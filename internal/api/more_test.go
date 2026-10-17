package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

// data-model.md section 5, test 3: a reader's author ID differs per site,
// in the widget and in exports.
func TestAuthorIDsDifferAcrossSites(t *testing.T) {
	e := newEnv(t)
	a, _ := e.popupLogin("ann", e.site)
	b, _ := e.popupLogin("ann", e.other)
	idA := decode(t, e.post(a, e.site, "on one site", nil))["author"].(map[string]any)["id"]
	in := map[string]any{"site": e.other.PublicID, "key": "p:/x", "url": "https://other.example/x", "title": "X", "body": "on another"}
	idB := decode(t, e.do(req{method: "POST", path: "/api/v1/comments", body: in, headers: bearer(b)}))["author"].(map[string]any)["id"]
	if idA == nil || idA == idB {
		t.Fatalf("author IDs: %v and %v", idA, idB)
	}
	u, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann")
	if idA == u.PublicID || idB == u.PublicID {
		t.Error("the account's own ID appears in the widget")
	}
	if err := e.st.AddMember(t.Context(), e.site.ID, u.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	session, _ := e.consoleLogin("ann")
	export := e.do(req{method: "GET", path: "/api/v1/sites/" + e.site.PublicID + "/export", cookies: []*http.Cookie{session}}).Body.String()
	if !strings.Contains(export, idA.(string)) || strings.Contains(export, idB.(string)) || strings.Contains(export, u.PublicID) {
		t.Error("an export links the reader across sites")
	}
}

func TestThreadMoveAndUndo(t *testing.T) {
	e := newEnv(t)
	session, csrfToken := e.consoleLogin("owner")
	o, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	if err := e.st.AddMember(t.Context(), e.site.ID, o.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	token, _ := e.popupLogin("ann", e.site)
	e.post(token, e.site, "old address", nil)
	h, c := csrf(csrfToken, session)
	base := "/api/v1/sites/" + e.site.PublicID
	threads := decode(t, e.do(req{method: "GET", path: base + "/threads", cookies: c}))["data"].([]any)
	from := threads[0].(map[string]any)["id"].(string)
	w := e.do(req{method: "POST", path: base + "/threads/" + from + "/move", body: map[string]string{"to_url": "https://blog.example/posts/b/"}, headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["key"] != "p:/posts/b" {
		t.Fatalf("move = %d %s", w.Code, w.Body)
	}
	moved := decode(t, e.do(req{method: "GET", path: "/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape("p:/posts/b")}))
	if moved["count"].(float64) != 1 {
		t.Fatalf("after move = %v", moved)
	}
	events := decode(t, e.do(req{method: "GET", path: base + "/events", cookies: c}))["data"].([]any)
	var moveID string
	for _, ev := range events {
		if ev.(map[string]any)["action"] == "move_thread" {
			moveID = ev.(map[string]any)["id"].(string)
		}
	}
	if w := e.do(req{method: "POST", path: base + "/events/" + moveID + "/undo", headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Fatalf("undo = %d %s", w.Code, w.Body)
	}
	back := decode(t, e.do(req{method: "GET", path: "/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey)}))
	if back["count"].(float64) != 1 {
		t.Errorf("after undo = %v", back)
	}
	if w := e.do(req{method: "POST", path: base + "/events/" + moveID + "/undo", headers: h, cookies: c}); w.Code != http.StatusBadRequest {
		t.Errorf("second undo = %d", w.Code)
	}
}

func TestGuestCommentsAreGone(t *testing.T) {
	e := newEnv(t)
	in := map[string]any{"site": e.site.PublicID, "key": blogKey, "url": blogPage, "title": "A", "body": "as a guest", "lang": "en",
		"guest": map[string]any{"name": "Gail", "email": "gail@example.com", "notify": true}}
	w := e.do(req{method: "POST", path: "/api/v1/comments", body: in, headers: own()})
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "guest_comments_gone") {
		t.Errorf("guest comment = %d %s", w.Code, w.Body)
	}
	delete(in, "guest")
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: in, headers: own()}); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out = %d", w.Code)
	}
	if w := e.do(req{method: "GET", path: "/api/v1/pow/challenge?site=" + e.site.PublicID, headers: own()}); w.Code != http.StatusGone {
		t.Errorf("proof of work challenge = %d", w.Code)
	}
	var n int
	_ = e.db.QueryRow(context.Background(), `SELECT count(*) FROM comments`).Scan(&n)
	if n != 0 {
		t.Errorf("%d comments stored", n)
	}
}

// Guests who asked for reply mail before guest comments ended keep it
// (moderation.md section 6.1).
func TestEarlierGuestKeepsReplyMail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	email := "gail@example.com"
	hmac := e.keys.EmailHMAC(email)
	g, err := e.st.CreateComment(ctx, store.NewComment{SiteID: e.site.ID, ThreadKey: blogKey, ThreadURL: blogPage, AuthorKind: model.AuthorGuest,
		AuthorName: "Gail", AuthorEmailEnc: e.keys.Encrypt(email), AuthorEmailHMAC: hmac,
		BodyMD: "as a guest", BodyHTML: "<p>as a guest</p>", RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	// The confirmation link mailed then still works.
	token := crypt.Token()
	if _, err := e.db.Exec(ctx, `INSERT INTO guest_emails (email_hmac, email_enc, locale, confirm_sent_at) VALUES ($1, $2, 'en', now())`,
		hmac, e.keys.Encrypt(email)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, `INSERT INTO guest_consents (email_hmac, browser_hash, comment_id, confirm_token_hash) VALUES ($1, $2, $3, $4)`,
		hmac, crypt.Hash(crypt.Token()), g.ID, crypt.Hash(token)); err != nil {
		t.Fatal(err)
	}
	if w := e.do(req{method: "GET", path: "/guest-email/confirm?t=" + token}); w.Code != http.StatusOK {
		t.Fatalf("confirm page = %d", w.Code)
	}
	var confirmed bool
	_ = e.db.QueryRow(ctx, `SELECT confirmed_at IS NOT NULL FROM guest_emails`).Scan(&confirmed)
	if confirmed {
		t.Fatal("opening the link confirmed the address")
	}
	if w := e.do(req{method: "POST", path: "/guest-email/confirm", body: url.Values{"t": {token}}}); w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	var notify bool
	_ = e.db.QueryRow(ctx, `SELECT notify_email FROM comments WHERE id = $1`, g.ID).Scan(&notify)
	if !notify {
		t.Fatal("the confirmed comment gets no reply mail")
	}
	ann, _ := e.popupLogin("ann", e.site)
	if w := e.post(ann, e.site, "a reply", map[string]any{"parent_id": strconv.FormatInt(g.ID, 10)}); w.Code != http.StatusCreated {
		t.Fatalf("reply = %d %s", w.Code, w.Body)
	}
	var mails int
	_ = e.db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE kind = 'guest_reply'`).Scan(&mails)
	if mails != 1 {
		t.Errorf("guest reply mails = %d", mails)
	}
}
