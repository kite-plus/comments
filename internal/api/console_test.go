package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

func csrf(token string, cookies ...*http.Cookie) (map[string]string, []*http.Cookie) {
	return map[string]string{"X-CSRF-Token": token}, cookies
}

func TestConsoleSession(t *testing.T) {
	e := newEnv(t)
	session, token := e.consoleLogin("ann")
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("console cookie = %+v", session)
	}
	body := map[string]any{"display_name": "Ann \u200bB", "homepage_url": "https://ann.example", "locale": "en"}
	if w := e.do(req{method: "PATCH", path: "/api/v1/me", body: body, cookies: []*http.Cookie{session}}); w.Code != http.StatusForbidden {
		t.Errorf("write without CSRF token = %d", w.Code)
	}
	h, c := csrf(token, session)
	w := e.do(req{method: "PATCH", path: "/api/v1/me", body: body, headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["display_name"] != "Ann B" {
		t.Fatalf("profile update = %d %s", w.Code, w.Body)
	}
	if w := e.do(req{method: "PATCH", path: "/api/v1/me", body: map[string]any{"homepage_url": "javascript:alert(1)"}, headers: h, cookies: c}); w.Code != http.StatusBadRequest {
		t.Errorf("javascript homepage = %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/auth/logout", headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	if w := e.do(req{method: "GET", path: "/api/v1/me", cookies: c}); w.Code != http.StatusUnauthorized {
		t.Errorf("session after logout = %d", w.Code)
	}
	// The console's return path stays on this origin.
	w = e.do(req{method: "GET", path: "/auth/login?return=" + url.QueryEscape("https://evil.example/")})
	binder := cookie(w, "comments_binder")
	w = e.do(req{method: "GET", path: e.authorize(w.Header().Get("Location")), cookies: []*http.Cookie{binder}})
	if w.Header().Get("Location") != "/me" {
		t.Errorf("console login returned to %q", w.Header().Get("Location"))
	}
}

func TestReaderCenterSessions(t *testing.T) {
	e := newEnv(t)
	tokenA, _ := e.popupLogin("ann", e.site)
	tokenB, _ := e.popupLogin("ann", e.other)
	session, token := e.consoleLogin("ann")
	h, c := csrf(token, session)
	w := e.do(req{method: "GET", path: "/api/v1/me/site-sessions", cookies: c})
	list := decode(t, w)["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("site sessions = %s", w.Body)
	}
	first := list[0].(map[string]any)
	if first["client"] == "" || first["site"].(map[string]any)["name"] == "" {
		t.Errorf("session entry = %v", first)
	}
	if w := e.do(req{method: "DELETE", path: "/api/v1/me/site-sessions/" + first["id"].(string), headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Fatalf("end one session = %d", w.Code)
	}
	w = e.do(req{method: "DELETE", path: "/api/v1/me/site-sessions", headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["ended"].(float64) != 1 {
		t.Fatalf("sign out of all sites = %d %s", w.Code, w.Body)
	}
	for _, x := range []struct {
		token string
		site  model.Site
	}{{tokenA, e.site}, {tokenB, e.other}} {
		if decode(t, e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + x.site.PublicID, headers: bearer(x.token)}))["viewer"] != nil {
			t.Error("a site session survived signing out of all sites")
		}
	}
}

func TestCommentsInTheReaderCenterAndExport(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	w := e.post(token, e.site, "**mine**", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("post = %d %s", w.Code, w.Body)
	}
	session, csrfToken := e.consoleLogin("ann")
	w = e.do(req{method: "GET", path: "/api/v1/me/comments", cookies: []*http.Cookie{session}})
	data := decode(t, w)["data"].([]any)
	if len(data) != 1 || !strings.HasSuffix(data[0].(map[string]any)["url"].(string), "#kc-"+data[0].(map[string]any)["id"].(string)) {
		t.Fatalf("my comments = %s", w.Body)
	}
	w = e.do(req{method: "GET", path: "/api/v1/me/export", cookies: []*http.Cookie{session}})
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(w.Body.String(), `"source":"**mine**"`) || strings.Contains(w.Body.String(), "csrf") {
		t.Errorf("export = %d %s", w.Code, w.Body)
	}
	// Deleting this service's data leaves the account and anonymizes.
	h, c := csrf(csrfToken, session)
	if w := e.do(req{method: "DELETE", path: "/api/v1/me", body: map[string]string{"comments": "anonymize"}, headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Fatalf("delete my data = %d %s", w.Code, w.Body)
	}
	page := decode(t, e.do(req{method: "GET", path: "/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey)}))
	author := page["data"].([]any)[0].(map[string]any)["author"].(map[string]any)
	if author["kind"] != "former_user" || author["name"] != "" || author["id"] != nil {
		t.Errorf("anonymized author = %v", author)
	}
}

func TestSiteRegistrationAndVerification(t *testing.T) {
	e := newEnv(t)
	e.srv.DevHTTPOrigins = true
	var token string
	blog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><head><meta name="kite-comments-site" content="%s"></head><body></body></html>`, token)
	}))
	defer blog.Close()
	session, csrfToken := e.consoleLogin("owner")
	h, c := csrf(csrfToken, session)
	w := e.do(req{method: "POST", path: "/api/v1/sites", body: map[string]any{"name": "Mine", "origin": blog.URL}, headers: h, cookies: c})
	if w.Code != http.StatusCreated {
		t.Fatalf("create site = %d %s", w.Code, w.Body)
	}
	site := decode(t, w)
	id := site["id"].(string)
	if _, ok := site["guest_policy"]; ok || site["status"] != "unverified" || site["role"] != "owner" {
		t.Errorf("new site = %v", site)
	}
	// Another account may claim the origin until one of them verifies it.
	session2, csrf2 := e.consoleLogin("squatter")
	h2, c2 := csrf(csrf2, session2)
	w = e.do(req{method: "POST", path: "/api/v1/sites", body: map[string]any{"name": "Again", "origin": blog.URL}, headers: h2, cookies: c2})
	if w.Code != http.StatusCreated {
		t.Fatalf("unverified origin blocked a second site = %d %s", w.Code, w.Body)
	}
	squat := decode(t, w)["id"].(string)
	w = e.do(req{method: "POST", path: "/api/v1/sites/" + id + "/verification/check", headers: h, cookies: c})
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("check without a token = %d %s", w.Code, w.Body)
	}
	w = e.do(req{method: "POST", path: "/api/v1/sites/" + id + "/verification", headers: h, cookies: c})
	token = decode(t, w)["token"].(string)
	w = e.do(req{method: "POST", path: "/api/v1/sites/" + id + "/verification/check", headers: h, cookies: c})
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d %s", w.Code, w.Body)
	}
	got := decode(t, w)
	if got["results"].([]any)[0].(map[string]any)["method"] != "meta" || got["site"].(map[string]any)["status"] != "pending_review" {
		t.Errorf("after check = %v", got)
	}
	// The squatter serves the same page but the origin is taken.
	w = e.do(req{method: "POST", path: "/api/v1/sites/" + squat + "/verification", headers: h2, cookies: c2})
	token = decode(t, w)["token"].(string)
	w = e.do(req{method: "POST", path: "/api/v1/sites/" + squat + "/verification/check", headers: h2, cookies: c2})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "已被另一个站点验证") {
		t.Errorf("second verification of an origin = %d %s", w.Code, w.Body)
	}
	if w := e.do(req{method: "POST", path: "/api/v1/sites", body: map[string]any{"name": "Third", "origin": blog.URL}, headers: h2, cookies: c2}); w.Code != http.StatusConflict {
		t.Errorf("site on a verified origin = %d", w.Code)
	}
	// The platform approves; only a platform admin may.
	if w := e.do(req{method: "POST", path: "/api/v1/admin/sites/" + id + "/approve", headers: h, cookies: c}); w.Code != http.StatusForbidden {
		t.Errorf("approval by the owner = %d", w.Code)
	}
	u, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	if err := e.st.SetPlatformAdmin(t.Context(), u.ID, true); err != nil {
		t.Fatal(err)
	}
	w = e.do(req{method: "POST", path: "/api/v1/admin/sites/" + id + "/approve", headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["status"] != "active" {
		t.Fatalf("approve = %d %s", w.Code, w.Body)
	}
	// Settings within the limits of moderation.md section 3.4.
	w = e.do(req{method: "PATCH", path: "/api/v1/sites/" + id, body: map[string]any{"query_keys": []string{"p", "p", "lang"}, "close_after_days": 30, "blocked_words": []string{" spam ", "SPAM"}}, headers: h, cookies: c})
	got = decode(t, w)
	if w.Code != http.StatusOK || len(got["query_keys"].([]any)) != 2 || got["close_after_days"].(float64) != 30 || len(got["blocked_words"].([]any)) != 1 {
		t.Errorf("settings = %d %s", w.Code, w.Body)
	}
	if w := e.do(req{method: "PATCH", path: "/api/v1/sites/" + id, body: map[string]any{"query_keys": []string{"a", "b", "c", "d", "e", "f"}}, headers: h, cookies: c}); w.Code != http.StatusBadRequest {
		t.Errorf("six query keys = %d", w.Code)
	}
	// Another reader cannot see the site.
	other, _ := e.consoleLogin("bob")
	if w := e.do(req{method: "GET", path: "/api/v1/sites/" + id, cookies: []*http.Cookie{other}}); w.Code != http.StatusNotFound {
		t.Errorf("another reader's view = %d", w.Code)
	}
}

func TestModerationInTheConsole(t *testing.T) {
	e := newEnv(t)
	ownerSession, ownerCSRF := e.consoleLogin("owner")
	o, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	if err := e.st.AddMember(t.Context(), e.site.ID, o.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	token, _ := e.popupLogin("bob", e.site)
	first := decode(t, e.post(token, e.site, "first", nil))
	second := decode(t, e.post(token, e.site, "second", nil))
	h, c := csrf(ownerCSRF, ownerSession)
	base := "/api/v1/sites/" + e.site.PublicID
	w := e.do(req{method: "POST", path: base + "/comments/moderation", body: map[string]any{"ids": []string{first["id"].(string), second["id"].(string)}, "action": "hide"}, headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["changed"].(float64) != 2 {
		t.Fatalf("bulk hide = %d %s", w.Code, w.Body)
	}
	w = e.do(req{method: "GET", path: base + "/comments?status=hidden", cookies: c})
	if n := len(decode(t, w)["data"].([]any)); n != 2 {
		t.Errorf("hidden queue = %d", n)
	}
	// Ban the reader by a comment of theirs; they can no longer post.
	w = e.do(req{method: "POST", path: base + "/bans", body: map[string]any{"kind": "user", "comment_id": first["id"], "reason": "test", "days": 7}, headers: h, cookies: c})
	if w.Code != http.StatusCreated {
		t.Fatalf("ban = %d %s", w.Code, w.Body)
	}
	if w := e.post(token, e.site, "after the ban", nil); w.Code != http.StatusForbidden {
		t.Errorf("banned reader posted: %d", w.Code)
	}
	w = e.do(req{method: "GET", path: base + "/bans", cookies: c})
	bans := decode(t, w)["data"].([]any)
	if len(bans) != 1 || bans[0].(map[string]any)["kind"] != "user" {
		t.Fatalf("bans = %s", w.Body)
	}
	if w := e.do(req{method: "DELETE", path: base + "/bans/" + bans[0].(map[string]any)["id"].(string), headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Errorf("unban = %d", w.Code)
	}
	if w := e.post(token, e.site, "after the unban", nil); w.Code != http.StatusCreated {
		t.Errorf("post after unban = %d %s", w.Code, w.Body)
	}
	w = e.do(req{method: "GET", path: base + "/events", cookies: c})
	actions := map[string]bool{}
	for _, ev := range decode(t, w)["data"].([]any) {
		actions[ev.(map[string]any)["action"].(string)] = true
	}
	if !actions["hide"] || !actions["ban"] || !actions["unban"] {
		t.Errorf("moderation record = %v", actions)
	}
}

func TestReportsAndWidgetModeration(t *testing.T) {
	e := newEnv(t)
	o := newManager(t, e, "owner")
	bob, _ := e.popupLogin("bob", e.site)
	c := decode(t, e.post(bob, e.site, "reported comment", nil))
	ann, _ := e.popupLogin("ann", e.site)
	r := map[string]any{"site": e.site.PublicID, "reason": "illegal", "note": "see"}
	if w := e.do(req{method: "POST", path: "/api/v1/comments/" + c["id"].(string) + "/reports", body: r, headers: bearer(ann)}); w.Code != http.StatusCreated {
		t.Fatalf("report = %d %s", w.Code, w.Body)
	}
	// Moderation in the widget needs a manager.
	act := map[string]any{"site": e.site.PublicID, "action": "hide"}
	if w := e.do(req{method: "POST", path: "/api/v1/comments/" + c["id"].(string) + "/moderation", body: act, headers: bearer(ann)}); w.Code != http.StatusForbidden {
		t.Errorf("a reader moderated: %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/api/v1/comments/" + c["id"].(string) + "/moderation", body: act, headers: bearer(o)}); w.Code != http.StatusOK {
		t.Fatalf("owner hides = %d %s", w.Code, w.Body)
	}
	w := e.do(req{method: "GET", path: "/api/v1/widget/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey), headers: bearer(o)})
	data := decode(t, w)["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["status"] != "hidden" {
		t.Errorf("manager read = %s", w.Body)
	}
	// The public read no longer shows it.
	pub := decode(t, e.do(req{method: "GET", path: "/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey)}))
	if len(pub["data"].([]any)) != 0 || pub["count"].(float64) != 0 {
		t.Errorf("public read = %v", pub)
	}
	// Locking closes the thread.
	if w := e.do(req{method: "PUT", path: "/api/v1/threads/lock", body: map[string]any{"site": e.site.PublicID, "key": blogKey, "locked": true}, headers: bearer(o)}); w.Code != http.StatusOK {
		t.Fatalf("lock = %d", w.Code)
	}
	if w := e.post(ann, e.site, "too late", nil); w.Code != http.StatusForbidden || decode(t, w)["error"].(map[string]any)["code"] != "thread_locked" {
		t.Errorf("post to a locked thread = %d %s", w.Code, w.Body)
	}
	// The urgent report mailed the owner at once.
	var n int
	_ = e.db.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE kind = 'report'`).Scan(&n)
	if n != 1 {
		t.Errorf("report mail queued = %d", n)
	}
}

// newManager signs a reader in to the widget as an owner of the site.
func newManager(t *testing.T, e *env, sub string) string {
	t.Helper()
	token, _ := e.popupLogin(sub, e.site)
	u, err := e.st.UserBySubject(t.Context(), e.idp.Issuer, sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddMember(t.Context(), e.site.ID, u.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestCommentRules(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	if w := e.post(token, e.site, strings.Repeat("x", 5001), nil); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "comment_too_long") {
		t.Errorf("too long = %d %s", w.Code, w.Body)
	}
	links := strings.Repeat("https://a.example/x ", 6)
	if w := e.post(token, e.site, links, nil); !strings.Contains(w.Body.String(), "too_many_links") {
		t.Errorf("too many links = %d %s", w.Code, w.Body)
	}
	if w := e.post(token, e.site, "hi", map[string]any{"url": "https://evil.example/posts/a/", "key": "p:/posts/a"}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "origin_not_allowed") {
		t.Errorf("foreign page = %d %s", w.Code, w.Body)
	}
	if w := e.post(token, e.site, "hi", map[string]any{"key": "p:/other"}); w.Code != http.StatusBadRequest {
		t.Errorf("key that does not match the page = %d", w.Code)
	}
	w := e.post(token, e.site, "a new account with https://link.example", nil)
	if w.Code != http.StatusCreated || decode(t, w)["status"] != "pending" {
		t.Errorf("new account's link = %d %s", w.Code, w.Body)
	}
	if w := e.post(token, e.site, "a new account with https://link.example", nil); w.Code != http.StatusConflict {
		t.Errorf("duplicate = %d", w.Code)
	}
	// Editing within the window, back through the rules.
	w = e.post(token, e.site, "plain", nil)
	c := decode(t, w)
	id := c["id"].(string)
	src := e.do(req{method: "GET", path: "/api/v1/comments/" + id + "/source?site=" + e.site.PublicID, headers: bearer(token)})
	if decode(t, src)["body"] != "plain" {
		t.Errorf("source = %s", src.Body)
	}
	w = e.do(req{method: "PATCH", path: "/api/v1/comments/" + id, body: map[string]any{"site": e.site.PublicID, "body": "*edited*"}, headers: bearer(token)})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<em>edited</em>") || decode(t, w)["edited_at"] == nil {
		t.Errorf("edit = %d %s", w.Code, w.Body)
	}
	other, _ := e.popupLogin("bob", e.site)
	if w := e.do(req{method: "PATCH", path: "/api/v1/comments/" + id, body: map[string]any{"site": e.site.PublicID, "body": "x"}, headers: bearer(other)}); w.Code != http.StatusNotFound {
		t.Errorf("someone else edited: %d", w.Code)
	}
	if _, err := e.db.Exec(context.Background(), `UPDATE comments SET created_at = now() - interval '25 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if w := e.do(req{method: "PATCH", path: "/api/v1/comments/" + id, body: map[string]any{"site": e.site.PublicID, "body": "late"}, headers: bearer(token)}); w.Code != http.StatusForbidden {
		t.Errorf("edit after the window = %d", w.Code)
	}
	if w := e.do(req{method: "DELETE", path: "/api/v1/comments/" + id + "?site=" + e.site.PublicID, headers: bearer(token)}); w.Code != http.StatusNoContent {
		t.Errorf("delete = %d", w.Code)
	}
}

func TestMailLinksActOnlyOnPost(t *testing.T) {
	e := newEnv(t)
	e.popupLogin("ann", e.site)
	u, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann")
	link := mailer.UnsubscribeURL(testPublic, e.keys, fmt.Sprintf("%s:%d", mailer.UnsubReplies, u.ID))
	path := strings.TrimPrefix(link, testPublic)
	if w := e.do(req{method: "GET", path: path}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `method="post"`) {
		t.Fatalf("unsubscribe page = %d", w.Code)
	}
	if u, _ := e.st.User(t.Context(), u.ID); !u.NotifyReplies {
		t.Fatal("opening the link unsubscribed")
	}
	// RFC 8058: the mail client posts to the link itself.
	if w := e.do(req{method: "POST", path: path, body: url.Values{"List-Unsubscribe": {"One-Click"}}}); w.Code != http.StatusOK {
		t.Fatalf("one-click = %d", w.Code)
	}
	if u, _ := e.st.User(t.Context(), u.ID); u.NotifyReplies {
		t.Error("one-click did not unsubscribe")
	}
	if w := e.do(req{method: "POST", path: "/unsubscribe?t=forged.token"}); w.Code != http.StatusBadRequest {
		t.Errorf("forged token = %d", w.Code)
	}
}

func TestClaimNeedsTheAccountThatStartedIt(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	_, err := e.st.CreateComment(ctx, store.NewComment{SiteID: e.site.ID, ThreadKey: blogKey, ThreadURL: blogPage, AuthorKind: model.AuthorGuest,
		AuthorName: "Old Me", AuthorEmailEnc: e.keys.Encrypt("old@example.com"), AuthorEmailHMAC: e.keys.EmailHMAC("old@example.com"),
		BodyMD: "from long ago", BodyHTML: "<p>from long ago</p>", RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	ann, annCSRF := e.consoleLogin("ann")
	h, c := csrf(annCSRF, ann)
	for _, email := range []string{"old@example.com", "nobody@example.com"} {
		if w := e.do(req{method: "POST", path: "/api/v1/me/claims", body: map[string]string{"email": email}, headers: h, cookies: c}); w.Code != http.StatusAccepted {
			t.Errorf("claim of %s = %d; it must not tell whether the address left comments", email, w.Code)
		}
	}
	var raw []byte
	if err := e.db.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE kind = 'claim' ORDER BY id LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p struct {
		ClaimID int64  `json:"claim_id"`
		Token   string `json:"token"`
	}
	_ = json.Unmarshal(raw, &p)
	verify := fmt.Sprintf("/api/v1/me/claims/%d/verify", p.ClaimID)
	bob, bobCSRF := e.consoleLogin("bob")
	bh, bc := csrf(bobCSRF, bob)
	if w := e.do(req{method: "POST", path: verify, body: map[string]string{"token": p.Token}, headers: bh, cookies: bc}); w.Code != http.StatusNotFound {
		t.Errorf("another account verified the claim: %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: verify, body: map[string]string{"token": "wrong"}, headers: h, cookies: c}); w.Code != http.StatusNotFound {
		t.Errorf("wrong token = %d", w.Code)
	}
	w := e.do(req{method: "POST", path: verify, body: map[string]string{"token": p.Token}, headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["count"].(float64) != 1 {
		t.Fatalf("verify = %d %s", w.Code, w.Body)
	}
	w = e.do(req{method: "POST", path: fmt.Sprintf("/api/v1/me/claims/%d/confirm", p.ClaimID), headers: h, cookies: c})
	if w.Code != http.StatusOK || decode(t, w)["claimed"].(float64) != 1 {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	page := decode(t, e.do(req{method: "GET", path: "/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey)}))
	author := page["data"].([]any)[0].(map[string]any)["author"].(map[string]any)
	if author["kind"] != "user" || author["name"] != "Ann" {
		t.Errorf("claimed comment author = %v", author)
	}
	var plain int
	_ = e.db.QueryRow(context.Background(), `SELECT count(*) FROM comments WHERE author_email_enc IS NOT NULL`).Scan(&plain)
	if plain != 0 {
		t.Error("the guest address survived the claim")
	}
}

func TestEmailsNeverStoredInPlainText(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	e.post(token, e.site, "hello", nil)
	session, csrfToken := e.consoleLogin("bob")
	// An import keeps its parsed comments for a day; their addresses too
	// must be encrypted.
	bob, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "bob")
	if err := e.st.AddMember(t.Context(), e.site.ID, bob.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	h, c := csrf(csrfToken, session)
	body, ct := multipartImport(t, "waline", walineExport)
	if w := e.do(req{method: "POST", path: "/api/v1/sites/" + e.site.PublicID + "/imports", body: body, headers: with(h, "Content-Type", ct), cookies: c}); w.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", w.Code, w.Body)
	}
	rows, err := e.db.Query(context.Background(), `SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND data_type IN ('text', 'jsonb', 'ARRAY', 'bytea')`)
	if err != nil {
		t.Fatal(err)
	}
	var cols [][3]string
	for rows.Next() {
		var tbl, col, typ string
		_ = rows.Scan(&tbl, &col, &typ)
		cols = append(cols, [3]string{tbl, col, typ})
	}
	rows.Close()
	for _, c := range cols {
		var n int
		q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s::text ILIKE '%%@example.com%%'`, c[0], c[1])
		if c[2] == "bytea" {
			q = fmt.Sprintf(`SELECT count(*) FROM %s WHERE position('@example.com'::bytea IN %s) > 0`, c[0], c[1])
		}
		if err := e.db.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			t.Errorf("%s.%s holds a plain address", c[0], c[1])
		}
	}
}
