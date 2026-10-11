package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/idp"
	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/store/storetest"
	"github.com/kite-plus/comments/internal/verify"
)

type box struct {
	mu  sync.Mutex
	got []mailer.Message
}

func (b *box) Send(_ context.Context, m mailer.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, m)
	return nil
}

var db *pgx.Conn

func setup(t *testing.T) (*Worker, *box, *store.Store) {
	t.Helper()
	st, schema := storetest.NewWithSchema(t)
	db = storetest.Conn(t, schema)
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	d, _ := crypt.ParseDataKeys("1:" + k)
	keys, err := crypt.New(d, bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	b := &box{}
	w := &Worker{Store: st, Keys: keys, Mailer: b, PublicURL: "https://comments.example", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return w, b, st
}

func user(t *testing.T, w *Worker, sub, name, email string) model.User {
	t.Helper()
	u, _, err := w.Store.LoginUser(context.Background(), store.OIDCLogin{Issuer: "https://id.example", Subject: sub, DisplayName: name,
		Locale: "en", EmailEnc: w.Keys.Encrypt(email), EmailHMAC: w.Keys.EmailHMAC(email)})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func comment(t *testing.T, st *store.Store, site model.Site, u model.User, parent *int64, body string) model.Comment {
	t.Helper()
	c, err := st.CreateComment(context.Background(), store.NewComment{SiteID: site.ID, ThreadKey: "p:/a", ThreadURL: "https://blog.example/a",
		ThreadTitle: "Post A", ParentID: parent, AuthorKind: model.AuthorUser, AuthorUserID: &u.ID, BodyMD: body, BodyHTML: "<p>" + body + "</p>",
		RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReplyAndOwnerMail(t *testing.T) {
	w, b, st := setup(t)
	ctx := context.Background()
	owner := user(t, w, "o", "Owner", "owner@example.com")
	ann := user(t, w, "a", "Ann", "ann@example.com")
	bob := user(t, w, "b", "Bob", "bob@example.com")
	site, err := st.CreateSite(ctx, store.NewSite{Name: "Blog", Origins: []string{"https://blog.example"}, Verified: true, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	top := comment(t, st, site, ann, nil, "first")
	comment(t, st, site, bob, &top.ID, "reply one")
	comment(t, st, site, bob, &top.ID, "reply two")
	w.SendMail(ctx)
	w.Minutely(ctx)
	w.SendMail(ctx)
	var replies, owners []mailer.Message
	for _, m := range b.got {
		switch m.To {
		case "ann@example.com":
			replies = append(replies, m)
		case "owner@example.com":
			owners = append(owners, m)
		}
	}
	// Both replies in one mail (moderation.md section 6.1), with a mute link
	// per thread and one-click unsubscribe.
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "reply one") || !strings.Contains(replies[0].Text, "reply two") ||
		!strings.Contains(replies[0].Text, "/mute?t=") || !strings.HasPrefix(replies[0].Unsubscribe, "https://comments.example/unsubscribe?t=") {
		t.Fatalf("reply mail = %+v", replies)
	}
	if len(owners) != 1 || !strings.Contains(owners[0].Subject, "3 new comments") {
		t.Fatalf("owner mail = %+v", owners)
	}
	// Within the window, new comments wait for the next owner mail.
	comment(t, st, site, bob, nil, "later")
	w.Minutely(ctx)
	w.SendMail(ctx)
	n := 0
	for _, m := range b.got {
		if m.To == "owner@example.com" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("owner mails inside the window = %d", n)
	}
	// Daily digest sites get theirs at the digest hour only.
	daily := model.NotifyDaily
	if _, err := st.UpdateSite(ctx, site.ID, store.SiteSettings{OwnerNotify: &daily}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE site_members SET last_notified_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	w.Now = func() time.Time { return time.Date(2026, 10, 12, 0, 30, 0, 0, time.UTC) }
	w.Minutely(ctx)
	w.SendMail(ctx)
	if last := b.got[len(b.got)-1]; strings.Contains(last.Subject, "today") {
		t.Fatal("digest before the digest hour")
	}
	w.Now = func() time.Time { return time.Date(2026, 10, 12, 1, 5, 0, 0, time.UTC) }
	w.Minutely(ctx)
	w.SendMail(ctx)
	if last := b.got[len(b.got)-1]; last.To != "owner@example.com" || !strings.Contains(last.Subject, "today") {
		t.Errorf("digest = %+v", last)
	}
}

func TestRecheckDropsAnOriginAfterTheGrace(t *testing.T) {
	w, b, st := setup(t)
	ctx := context.Background()
	owner := user(t, w, "o", "Owner", "owner@example.com")
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(404) }))
	defer srv.Close()
	site, err := st.CreateSite(ctx, store.NewSite{Name: "Blog", Origins: []string{srv.URL}, Verified: true, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	w.Fetcher = verify.NewFetcher(true)
	w.Fetcher.LookupTXT = func(context.Context, string) ([]string, error) { return nil, nil }
	// Last checked long ago, failing for longer than the grace period.
	if _, err := db.Exec(ctx, `UPDATE site_origins SET last_checked_at = now() - interval '40 days', verified_at = now() - interval '60 days'`); err != nil {
		t.Fatal(err)
	}
	w.recheckOrigins(ctx)
	got, _ := st.SiteByID(ctx, site.ID)
	if got.Origins[0].FailingSince == nil || got.Origins[0].VerifiedAt == nil {
		t.Fatalf("after a first failure = %+v", got.Origins[0])
	}
	w.SendMail(ctx)
	if len(b.got) != 1 || !strings.Contains(b.got[0].Subject, "verification record") {
		t.Errorf("warning mail = %+v", b.got)
	}
	if _, err := db.Exec(ctx, `UPDATE site_origins SET last_checked_at = now() - interval '40 days', failing_since = now() - interval '15 days'`); err != nil {
		t.Fatal(err)
	}
	w.recheckOrigins(ctx)
	got, _ = st.SiteByID(ctx, site.ID)
	if got.Origins[0].VerifiedAt != nil || got.Status != model.SiteUnverified {
		t.Errorf("after the grace = %+v %s", got.Origins[0], got.Status)
	}
}

func TestImportedHomepagesAreWebAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.example/me": "https://a.example/me", " http://a.example ": "http://a.example",
		"javascript:alert(1)": "", "data:text/html,x": "", "//a.example": "", "": "",
	} {
		if got := webURL(in); got != want {
			t.Errorf("webURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReconcileRetriesASubjectItCannotRead(t *testing.T) {
	w, _, st := setup(t)
	ctx := context.Background()
	u := user(t, w, "sub-1", "Ann", "ann@example.com")
	var mu sync.Mutex
	readable := false
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/admin/v1/events/_search":
			_, _ = rw.Write([]byte(`{"events":[{"aggregate":{"id":"sub-1"},"sequence":"4","creationDate":"2026-10-11T06:00:00Z","type":{"type":"user.deactivated"}}]}`))
		case !readable:
			rw.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = rw.Write([]byte(`{"user":{"state":"USER_STATE_INACTIVE"}}`))
		}
	}))
	defer srv.Close()
	w.IDP = &idp.Client{BaseURL: srv.URL, Token: "t"}
	w.Issuer = "https://id.example"
	w.Reconcile(ctx)
	if x, _ := st.User(ctx, u.ID); x.Suspended() {
		t.Fatal("acted without reading the user")
	}
	cursor, _ := st.IDPCursor(ctx)
	if cursor != "2026-10-11T06:00:00Z" {
		t.Errorf("cursor moved past the unread event: %q", cursor)
	}
	mu.Lock()
	readable = true
	mu.Unlock()
	w.Reconcile(ctx)
	if x, _ := st.User(ctx, u.ID); x.IDPSuspendedAt == nil {
		t.Error("the next run did not retry the event")
	}
}

// More than a page of events inside the overlap window must not make
// every page start over at the window.
func TestReconcileGetsPastABurst(t *testing.T) {
	w, _, st := setup(t)
	ctx := context.Background()
	u := user(t, w, "real", "Ann", "ann@example.com")
	cursor := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	type event struct {
		sub, kind string
		seq       int
		at        time.Time
	}
	var events []event
	// 600 handled events in the five minutes before the cursor ...
	for i := range 600 {
		ev := event{sub: "old", kind: idp.UserDeactivated, seq: i, at: cursor.Add(-5*time.Minute + time.Duration(i+1)*400*time.Millisecond)}
		events = append(events, ev)
		if err := st.RecordIDPEvent(ctx, ev.sub+":"+strconv.Itoa(ev.seq), ev.kind); err != nil {
			t.Fatal(err)
		}
	}
	// ... then 700 new ones in the next minute, the last one for a reader.
	for i := range 700 {
		events = append(events, event{sub: "new", kind: idp.UserDeactivated, seq: i, at: cursor.Add(time.Duration(i+1) * 50 * time.Millisecond)})
	}
	events[len(events)-1].sub = "real"
	if err := st.SetIDPCursor(ctx, cursor.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	searches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/v1/events/_search" {
			_, _ = rw.Write([]byte(`{"user":{"state":"USER_STATE_INACTIVE"}}`))
			return
		}
		mu.Lock()
		searches++
		mu.Unlock()
		var q struct {
			From  time.Time `json:"from"`
			Limit int       `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var out []map[string]any
		for _, ev := range events {
			if ev.at.After(q.From) && len(out) < q.Limit {
				out = append(out, map[string]any{"aggregate": map[string]any{"id": ev.sub}, "sequence": strconv.Itoa(ev.seq),
					"creationDate": ev.at.Format(time.RFC3339Nano), "type": map[string]any{"type": ev.kind}})
			}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"events": out})
	}))
	defer srv.Close()
	w.IDP = &idp.Client{BaseURL: srv.URL, Token: "t"}
	w.Issuer = "https://id.example"
	w.Reconcile(ctx)
	if x, _ := st.User(ctx, u.ID); x.IDPSuspendedAt == nil {
		t.Error("the event after the burst was not applied")
	}
	if got, _ := st.IDPCursor(ctx); got != events[len(events)-1].at.Format(time.RFC3339Nano) {
		t.Errorf("cursor = %s", got)
	}
	if searches > 4 {
		t.Errorf("%d searches for 1300 events", searches)
	}
}

// Mail queued before a reader changed their mind follows the change.
func TestMailRechecksTheRecipient(t *testing.T) {
	w, b, st := setup(t)
	ctx := context.Background()
	owner := user(t, w, "o", "Owner", "owner@example.com")
	ann := user(t, w, "a", "Ann", "ann@example.com")
	bob := user(t, w, "b", "Bob", "bob@example.com")
	site, err := st.CreateSite(ctx, store.NewSite{Name: "Blog", Origins: []string{"https://blog.example"}, Verified: true, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	none := model.NotifyOff
	if _, err := st.UpdateSite(ctx, site.ID, store.SiteSettings{OwnerNotify: &none}); err != nil {
		t.Fatal(err)
	}
	top := comment(t, st, site, ann, nil, "first")
	sent := func() int {
		w.SendMail(ctx)
		b.mu.Lock()
		defer b.mu.Unlock()
		n := len(b.got)
		b.got = nil
		return n
	}
	// Muted after the reply was queued.
	comment(t, st, site, bob, &top.ID, "one")
	if err := st.Mute(ctx, ann.ID, top.ThreadID); err != nil {
		t.Fatal(err)
	}
	if n := sent(); n != 0 {
		t.Errorf("mail for a muted thread: %d", n)
	}
	if err := st.Unmute(ctx, ann.ID, top.ThreadID); err != nil {
		t.Fatal(err)
	}
	// Reply mail turned off after it was queued.
	comment(t, st, site, bob, &top.ID, "two")
	off, on := false, true
	if _, err := st.UpdateProfile(ctx, ann.ID, store.ProfileUpdate{NotifyReplies: &off}); err != nil {
		t.Fatal(err)
	}
	if n := sent(); n != 0 {
		t.Errorf("mail after opting out: %d", n)
	}
	if _, err := st.UpdateProfile(ctx, ann.ID, store.ProfileUpdate{NotifyReplies: &on}); err != nil {
		t.Fatal(err)
	}
	// The account is gone: its queued mail goes with it.
	comment(t, st, site, bob, &top.ID, "three")
	var queued int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE status = 'queued'`).Scan(&queued)
	if queued == 0 {
		t.Fatal("no reply mail queued")
	}
	if err := st.DeleteUserData(ctx, ann.ID, "remove", false); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE status = 'queued'`).Scan(&queued)
	if queued != 0 {
		t.Errorf("queued mail after deletion: %d", queued)
	}
	if n := sent(); n != 0 {
		t.Errorf("mail to a deleted account: %d", n)
	}
}

// A long or tricky title from the first commenter reaches other readers'
// mail cleaned, cut and escaped.
func TestMailTitlesAreCut(t *testing.T) {
	w, b, st := setup(t)
	ctx := context.Background()
	ann := user(t, w, "a", "Ann", "ann@example.com")
	bob := user(t, w, "b", "Bob", "bob@example.com")
	site, err := st.CreateSite(ctx, store.NewSite{Name: "Blog", Origins: []string{"https://blog.example"}, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	title := "<b>Win</b>\u202e " + strings.Repeat("x", 200)
	top, err := st.CreateComment(ctx, store.NewComment{SiteID: site.ID, ThreadKey: "p:/t", ThreadURL: "https://blog.example/t",
		ThreadTitle: title, AuthorKind: model.AuthorUser, AuthorUserID: &ann.ID, BodyMD: "a", BodyHTML: "<p>a</p>",
		RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateComment(ctx, store.NewComment{SiteID: site.ID, ThreadKey: "p:/t", ThreadURL: "https://blog.example/t",
		ParentID: &top.ID, AuthorKind: model.AuthorUser, AuthorUserID: &bob.ID, BodyMD: "b", BodyHTML: "<p>b</p>",
		RenderVersion: 1, Status: model.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	w.SendMail(ctx)
	if len(b.got) != 1 {
		t.Fatalf("mails = %d", len(b.got))
	}
	m := b.got[0]
	if strings.Contains(m.HTML, "<b>Win") || strings.Contains(m.Text, "\u202e") || strings.Contains(m.Text, strings.Repeat("x", 81)) ||
		!strings.Contains(m.Text, "x…") {
		t.Errorf("title in mail:\n%s", m.Text)
	}
}

func TestScrubKeepsAddressesOut(t *testing.T) {
	for err, want := range map[error]string{
		&textproto.Error{Code: 550, Msg: "5.1.1 <ann@example.com>: Recipient address rejected"}:         "smtp 550",
		fmt.Errorf("send: %w", &textproto.Error{Code: 452, Msg: "too many recipients bob@example.com"}): "smtp 452",
		errors.New("dial tcp: rcpt to:<ann@example.com> refused"):                                       "dial tcp: rcpt to:<<address>> refused",
	} {
		if got := scrub(err); got != want {
			t.Errorf("scrub(%v) = %q, want %q", err, got, want)
		}
	}
}
