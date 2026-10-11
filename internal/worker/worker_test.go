package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/crypt"
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
