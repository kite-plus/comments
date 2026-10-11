package idp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/store/storetest"
)

func TestSignature(t *testing.T) {
	body := []byte(`{"aggregateID":"1"}`)
	now := time.Unix(1791699417, 0)
	h := Sign(body, "key", now)
	if err := VerifySignature(h, body, "key", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(h+",v1=00", body, "key", now); err != nil {
		t.Errorf("rotation with an extra v1: %v", err)
	}
	for name, err := range map[string]error{
		"wrong key": VerifySignature(h, body, "other", now),
		"old":       VerifySignature(h, body, "key", now.Add(6*time.Minute)),
		"body":      VerifySignature(h, []byte(`{}`), "key", now),
		"no secret": VerifySignature(h, body, "", now),
		"no header": VerifySignature("", body, "key", now),
	} {
		if !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseWebhook(t *testing.T) {
	ev, err := ParseWebhook([]byte(`{"aggregateID":"394605853950385668","aggregateType":"user","resourceOwner":"1","instanceID":"2","version":"v2","sequence":3,"event_type":"user.deactivated","created_at":"2026-10-11T06:16:57.531491Z","userID":"9"}`))
	if err != nil || ev.ID != "394605853950385668:3" || ev.Subject != "394605853950385668" || ev.Type != UserDeactivated || ev.FromSearch {
		t.Fatalf("event = %+v, %v", ev, err)
	}
}

// fakeIDP answers the two API calls Apply makes.
type fakeIDP struct {
	mu      sync.Mutex
	state   string // "", or a USER_STATE_ value; "" answers not found
	removed bool
	down    bool
	html404 bool
}

func (f *fakeIDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/v2/users/"):
		if f.html404 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<html>not found</html>"))
			return
		}
		if f.state == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"User could not be found (QUERY-Dfbg2)"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"state": f.state}})
	case r.URL.Path == "/admin/v1/events/_search":
		if f.removed {
			_, _ = w.Write([]byte(`{"events":[{"aggregate":{"id":"sub"},"sequence":"9","type":{"type":"user.removed"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}
}

func (f *fakeIDP) set(fn func(*fakeIDP)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func TestApplyTreatsPushesAsHints(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	f := &fakeIDP{state: "USER_STATE_ACTIVE"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "t"}
	u, _, err := st.LoginUser(ctx, store.OIDCLogin{Issuer: "iss", Subject: "sub", DisplayName: "U", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	push := func(kind string) (bool, error) {
		seq++
		return Apply(ctx, st, c, "iss", Event{ID: "sub:" + string(rune('a'+seq)), Type: kind, Subject: "sub"})
	}
	user := func() model.User {
		x, err := st.User(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	// A forged removal while the user is active removes nothing.
	if _, err := push(UserRemoved); err != nil {
		t.Fatal(err)
	}
	if user().SuspendedAt != nil {
		t.Fatal("an active user was touched")
	}
	// Locking never suspends.
	f.set(func(f *fakeIDP) { f.state = "USER_STATE_LOCKED" })
	if _, err := push(UserLocked); err != nil || user().SuspendedAt != nil {
		t.Fatalf("lock suspended: %v", err)
	}
	f.set(func(f *fakeIDP) { f.state = "USER_STATE_INACTIVE" })
	if _, err := push(UserDeactivated); err != nil || user().SuspendedAt == nil {
		t.Fatalf("deactivation did not suspend: %v", err)
	}
	f.set(func(f *fakeIDP) { f.state = "USER_STATE_ACTIVE" })
	if _, err := push(UserReactivated); err != nil || user().SuspendedAt != nil {
		t.Fatalf("reactivation did not resume: %v", err)
	}
	// Unreadable identity service: nothing happens, the event is not
	// recorded, and the caller can answer 503.
	f.set(func(f *fakeIDP) { f.down = true })
	if _, err := push(UserDeactivated); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
	f.set(func(f *fakeIDP) { f.down, f.html404 = false, true })
	if _, err := push(UserRemoved); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a 404 that is not the user-not-found error: %v", err)
	}
	// Gone, but the event search has no removal yet: not confirmed.
	f.set(func(f *fakeIDP) { f.html404, f.state = false, "" })
	if _, err := push(UserRemoved); !errors.Is(err, ErrUnconfirmed) {
		t.Fatalf("unconfirmed removal: %v", err)
	}
	if _, err := st.User(ctx, u.ID); err != nil {
		t.Fatal("deleted without confirmation")
	}
	f.set(func(f *fakeIDP) { f.removed = true })
	if _, err := push(UserRemoved); err != nil {
		t.Fatal(err)
	}
	if _, err := st.User(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("confirmed removal kept the user: %v", err)
	}
}
