package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/pow"
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

func TestGuestCommentAndConfirmation(t *testing.T) {
	e := newEnv(t)
	policy := model.GuestPublish
	if _, err := e.st.UpdateSite(t.Context(), e.site.ID, store.SiteSettings{GuestPolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	solve := func() pow.Solution {
		w := e.do(req{method: "GET", path: "/api/v1/pow/challenge?site=" + e.site.PublicID, headers: own()})
		var ch pow.Challenge
		_ = json.Unmarshal(w.Body.Bytes(), &ch)
		n, ok := pow.Solve(ch)
		if !ok {
			t.Fatal("unsolvable challenge")
		}
		return pow.Solution{Challenge: ch.Challenge, Salt: ch.Salt, Signature: ch.Signature, Number: n}
	}
	browserA := crypt.Token()
	guest := map[string]any{"name": "Gail", "email": "gail@example.com", "notify": true, "token": browserA}
	body := func(text string, p *pow.Solution) map[string]any {
		return map[string]any{"site": e.site.PublicID, "key": blogKey, "url": blogPage, "title": "A", "body": text, "guest": guest, "pow": p, "lang": "en"}
	}
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: body("no work", nil), headers: own()}); w.Code != http.StatusBadRequest {
		t.Errorf("without proof of work = %d", w.Code)
	}
	sol := solve()
	w := e.do(req{method: "POST", path: "/api/v1/comments", body: body("as a guest", &sol), headers: own()})
	if w.Code != http.StatusCreated || decode(t, w)["author"].(map[string]any)["kind"] != "guest" {
		t.Fatalf("guest comment = %d %s", w.Code, w.Body)
	}
	first := decode(t, w)["id"].(string)
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: body("again", &sol), headers: own()}); w.Code != http.StatusBadRequest {
		t.Errorf("spent proof of work = %d", w.Code)
	}
	hp := map[string]any{"name": "Bot", "email": "bot@example.com", "website": "http://spam.example"}
	s2 := solve()
	in := body("bot", &s2)
	in["guest"] = hp
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: in, headers: own()}); w.Code != http.StatusBadRequest {
		t.Errorf("honeypot = %d", w.Code)
	}
	// One confirmation mail; the address gets no reply mail before it.
	var raw []byte
	if err := e.db.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE kind = 'guest_confirm'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(raw, &p)
	if w := e.do(req{method: "GET", path: "/guest-email/confirm?t=" + p.Token}); w.Code != http.StatusOK {
		t.Fatalf("confirm page = %d", w.Code)
	}
	var confirmed bool
	_ = e.db.QueryRow(context.Background(), `SELECT confirmed_at IS NOT NULL FROM guest_emails`).Scan(&confirmed)
	if confirmed {
		t.Fatal("opening the link confirmed the address")
	}
	if w := e.do(req{method: "POST", path: "/guest-email/confirm", body: url.Values{"t": {p.Token}}}); w.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	_ = e.db.QueryRow(context.Background(), `SELECT confirmed_at IS NOT NULL FROM guest_emails`).Scan(&confirmed)
	if !confirmed {
		t.Error("not confirmed")
	}
	notify := func(id string) bool {
		var on bool
		if err := e.db.QueryRow(context.Background(), `SELECT notify_email FROM comments WHERE id = $1`, id).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	mails := func() int {
		var n int
		_ = e.db.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE kind = 'guest_confirm'`).Scan(&n)
		return n
	}
	if !notify(first) {
		t.Error("the confirmed comment gets no reply mail")
	}
	// The same browser needs no new confirmation ...
	s4 := solve()
	w = e.do(req{method: "POST", path: "/api/v1/comments", body: body("from the same browser", &s4), headers: own()})
	if w.Code != http.StatusCreated || !notify(decode(t, w)["id"].(string)) || mails() != 1 {
		t.Errorf("same browser = %d, mails %d", w.Code, mails())
	}
	// ... another browser with the same address does, by mail.
	guest["token"] = crypt.Token()
	if _, err := e.db.Exec(context.Background(), `UPDATE guest_emails SET confirm_sent_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	s5 := solve()
	w = e.do(req{method: "POST", path: "/api/v1/comments", body: body("from another browser", &s5), headers: own()})
	if w.Code != http.StatusCreated || notify(decode(t, w)["id"].(string)) || mails() != 2 {
		t.Errorf("another browser = %d, mails %d", w.Code, mails())
	}
	// No token at all is never a confirmed browser.
	delete(guest, "token")
	if _, err := e.db.Exec(context.Background(), `UPDATE guest_emails SET confirm_sent_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	s6 := solve()
	w = e.do(req{method: "POST", path: "/api/v1/comments", body: body("no token", &s6), headers: own()})
	if w.Code != http.StatusCreated || notify(decode(t, w)["id"].(string)) || mails() != 3 {
		t.Errorf("no token = %d, mails %d", w.Code, mails())
	}
	// Guests are off on the other site.
	s3 := solve()
	off := body("elsewhere", &s3)
	off["site"], off["url"], off["key"] = e.other.PublicID, "https://other.example/x", "p:/x"
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: off, headers: own()}); w.Code != http.StatusForbidden {
		t.Errorf("guest on a site without guests = %d", w.Code)
	}
}
