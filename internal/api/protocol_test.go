package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/idp"
	"github.com/kite-plus/comments/internal/store"
)

func TestPopupLogin(t *testing.T) {
	e := newEnv(t)
	token, ck := e.popupLogin("ann", e.site)
	if ck == nil || !ck.Partitioned || !ck.Secure || ck.SameSite != http.SameSiteNoneMode || !ck.HttpOnly || ck.Path != "/" {
		t.Fatalf("site cookie = %+v", ck)
	}
	if ck.Value != token {
		t.Error("cookie and token differ")
	}
	// Either carrier is accepted; the cookie alone identifies the reader.
	w := e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: own(), cookies: []*http.Cookie{ck}})
	v, _ := decode(t, w)["viewer"].(map[string]any)
	if v == nil || v["name"] != "Ann" {
		t.Fatalf("viewer by cookie = %s", w.Body)
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("viewer cache = %q", w.Header().Get("Cache-Control"))
	}
	w = e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: bearer(token)})
	if v, _ := decode(t, w)["viewer"].(map[string]any); v == nil {
		t.Fatal("viewer by bearer token missing")
	}
	// The session is only good for its site.
	other := e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.other.PublicID, headers: bearer(token)})
	if decode(t, other)["viewer"] != nil {
		t.Error("session accepted on another site")
	}
	// Sign out of this site.
	w = e.do(req{method: "DELETE", path: "/api/v1/widget/session?site=" + e.site.PublicID, headers: bearer(token)})
	if w.Code != http.StatusNoContent {
		t.Fatalf("sign out = %d", w.Code)
	}
	if c := cookie(w, ck.Name); c == nil || c.MaxAge >= 0 {
		t.Errorf("sign out cookie = %+v", c)
	}
	w = e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: bearer(token)})
	if decode(t, w)["viewer"] != nil {
		t.Error("session survived sign out")
	}
}

func TestRedeemStates(t *testing.T) {
	e := newEnv(t)
	verifier, challenge := verifierPair()
	redeem := func() int {
		return e.do(req{method: "POST", path: "/api/v1/widget/sessions", body: map[string]string{"site": e.site.PublicID, "verifier": verifier}, headers: own()}).Code
	}
	if c := redeem(); c != http.StatusAccepted {
		t.Errorf("unknown challenge = %d, want 202", c)
	}
	e.idp.SignIn("ann")
	w := e.do(req{method: "POST", path: "/auth/transactions", body: map[string]string{"site": e.site.PublicID, "challenge": challenge}, headers: own()})
	binder := cookie(w, "comments_binder")
	if c := redeem(); c != http.StatusAccepted {
		t.Errorf("pending = %d, want 202", c)
	}
	e.do(req{method: "GET", path: e.authorize(decode(t, w)["authorize_url"].(string)), cookies: []*http.Cookie{binder}})
	if c := redeem(); c != http.StatusCreated {
		t.Errorf("finished = %d, want 201", c)
	}
	if c := redeem(); c != http.StatusGone {
		t.Errorf("second redeem = %d, want 410", c)
	}
	// The challenge cannot start another transaction.
	w = e.do(req{method: "POST", path: "/auth/transactions", body: map[string]string{"site": e.site.PublicID, "challenge": challenge}, headers: own()})
	if w.Code != http.StatusConflict {
		t.Errorf("reused challenge = %d", w.Code)
	}
}

// T8: pages other than the service's own cannot start a login.
func TestT8CrossSiteTransaction(t *testing.T) {
	e := newEnv(t)
	_, challenge := verifierPair()
	body := map[string]string{"site": e.site.PublicID, "challenge": challenge}
	cases := map[string]map[string]string{
		"foreign origin":       with(own(), "Origin", evilOrigin, "Sec-Fetch-Site", "cross-site"),
		"host page":            with(own(), "Origin", blogOrigin, "Sec-Fetch-Site", "cross-site"),
		"same site, not ours":  with(own(), "Sec-Fetch-Site", "same-site"),
		"no origin":            with(own(), "Origin", ""),
		"navigation":           with(own(), "Sec-Fetch-Mode", "navigate"),
		"origin spoof, header": with(own(), "Sec-Fetch-Site", "cross-site"),
	}
	for name, h := range cases {
		if w := e.do(req{method: "POST", path: "/auth/transactions", body: body, headers: h}); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	form := url.Values{"site": {e.site.PublicID}, "challenge": {challenge}}
	if w := e.do(req{method: "POST", path: "/auth/transactions", body: form, headers: own()}); w.Code == http.StatusOK {
		t.Error("form post accepted")
	}
	// The redirect entry takes only the widget's own form.
	form.Set("return", blogPage)
	if w := e.do(req{method: "POST", path: "/auth/redirect", body: form, headers: with(own(), "Origin", evilOrigin, "Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "navigate")}); w.Code != http.StatusForbidden {
		t.Errorf("redirect entry from another site = %d", w.Code)
	}
	// And returns only to the site's own pages.
	form.Set("return", evilOrigin+"/phish")
	if w := e.do(req{method: "POST", path: "/auth/redirect", body: form, headers: with(own(), "Sec-Fetch-Mode", "navigate")}); w.Code != http.StatusBadRequest {
		t.Errorf("open redirect = %d", w.Code)
	}
}

// T8: a transaction created with the attacker's own challenge and binder
// cannot be completed in the reader's browser, nor confirmed there.
func TestT8ForeignChallengeAndForgedCallback(t *testing.T) {
	e := newEnv(t)
	e.idp.SignIn("ann")
	verifier, challenge := verifierPair()
	// The attacker forges the headers from their server and keeps the binder.
	w := e.do(req{method: "POST", path: "/auth/transactions", body: map[string]string{"site": e.site.PublicID, "challenge": challenge}, headers: own()})
	attackerBinder := cookie(w, "comments_binder")
	cb := e.authorize(decode(t, w)["authorize_url"].(string))
	// The reader's browser arrives at the callback with its own binder.
	readerBinder := &http.Cookie{Name: "comments_binder", Value: strings.Repeat("r", 43)}
	if w := e.do(req{method: "GET", path: cb, cookies: []*http.Cookie{readerBinder}}); w.Code != http.StatusBadRequest {
		t.Errorf("callback with another browser's binder = %d", w.Code)
	}
	if w := e.do(req{method: "GET", path: cb}); w.Code != http.StatusBadRequest {
		t.Errorf("callback without a binder = %d", w.Code)
	}
	if w := e.do(req{method: "GET", path: "/auth/callback?state=forged&code=x", cookies: []*http.Cookie{attackerBinder}}); w.Code != http.StatusBadRequest {
		t.Errorf("forged state = %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/api/v1/widget/sessions", body: map[string]string{"site": e.site.PublicID, "verifier": verifier}, headers: own()}); w.Code != http.StatusAccepted {
		t.Errorf("attacker redeemed a login the reader never completed: %d", w.Code)
	}
}

func TestRedirectLoginWithConfirmation(t *testing.T) {
	e := newEnv(t)
	e.idp.SignIn("ann")
	verifier, challenge := verifierPair()
	form := url.Values{"site": {e.site.PublicID}, "challenge": {challenge}, "return": {blogPage + "#comments"}}
	w := e.do(req{method: "POST", path: "/auth/redirect", body: form, headers: with(own(), "Sec-Fetch-Mode", "navigate")})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("redirect entry = %d %s", w.Code, w.Body)
	}
	binder := cookie(w, "comments_binder")
	w = e.do(req{method: "GET", path: e.authorize(w.Header().Get("Location")), cookies: []*http.Cookie{binder}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/auth/confirm"`) {
		t.Fatalf("confirm page = %d %s", w.Code, w.Body)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, blogOrigin) || w.Header().Get("Cross-Origin-Opener-Policy") != "" {
		t.Errorf("confirm page headers: %v", w.Header())
	}
	txID := w.Body.String()[strings.Index(w.Body.String(), `name="tx" value="`)+len(`name="tx" value="`):]
	txID = txID[:strings.Index(txID, `"`)]
	// Not confirmed yet: nothing to redeem.
	redeem := map[string]string{"site": e.site.PublicID, "verifier": verifier}
	if w := e.do(req{method: "POST", path: "/api/v1/widget/sessions", body: redeem, headers: own()}); w.Code != http.StatusAccepted {
		t.Errorf("redeem before confirmation = %d", w.Code)
	}
	confirm := url.Values{"tx": {txID}}
	if w := e.do(req{method: "POST", path: "/auth/confirm", body: confirm, headers: with(own(), "Origin", evilOrigin, "Sec-Fetch-Site", "cross-site"), cookies: []*http.Cookie{binder}}); w.Code != http.StatusForbidden {
		t.Errorf("confirmation from another site = %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/auth/confirm", body: confirm, headers: own(), cookies: []*http.Cookie{{Name: "comments_binder", Value: strings.Repeat("x", 43)}}}); w.Code != http.StatusGone {
		t.Errorf("confirmation from another browser = %d", w.Code)
	}
	w = e.do(req{method: "POST", path: "/auth/confirm", body: confirm, headers: own(), cookies: []*http.Cookie{binder}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != blogPage+"#kc-login" {
		t.Fatalf("confirm = %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := e.do(req{method: "POST", path: "/api/v1/widget/sessions", body: redeem, headers: own()}); w.Code != http.StatusCreated {
		t.Errorf("redeem after confirmation = %d %s", w.Code, w.Body)
	}
}

// T8: the host page shares the partitioned cookie's jar, so writes must
// come from the service's own pages.
func TestT8CrossSiteWritesWithCookie(t *testing.T) {
	e := newEnv(t)
	_, ck := e.popupLogin("ann", e.site)
	body := map[string]any{"site": e.site.PublicID, "key": blogKey, "url": blogPage, "title": "A", "body": "forged"}
	for name, h := range map[string]map[string]string{
		"host page script":    {"Origin": blogOrigin, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors"},
		"no fetch metadata":   {"Origin": blogOrigin},
		"neither header":      {},
		"same-site subdomain": {"Origin": "http://x.comments.test", "Sec-Fetch-Site": "same-site"},
	} {
		w := e.do(req{method: "POST", path: "/api/v1/comments", body: body, headers: h, cookies: []*http.Cookie{ck}})
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// The viewer and sign-out need the same.
	if w := e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, cookies: []*http.Cookie{ck}}); w.Code != http.StatusForbidden {
		t.Errorf("cross-site viewer probe = %d", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/api/v1/comments", body: body, headers: own(), cookies: []*http.Cookie{ck}}); w.Code != http.StatusCreated {
		t.Errorf("the widget's own write = %d %s", w.Code, w.Body)
	}
}

// T8 and data-model.md section 5: anonymous reads never see a cookie and
// change nothing, not even a session's last use.
func TestT8ReadsCarryNoSession(t *testing.T) {
	e := newEnv(t)
	token, ck := e.popupLogin("ann", e.site)
	if w := e.post(token, e.site, "hello", nil); w.Code != http.StatusCreated {
		t.Fatalf("post = %d %s", w.Code, w.Body)
	}
	console, _ := e.consoleLogin("ann")
	snapshot := func() string {
		var out strings.Builder
		for _, table := range []string{"users", "sites", "threads", "comments", "site_sessions", "console_sessions", "login_transactions",
			"site_authors", "notifications", "outbox", "moderation_events", "idp_events"} {
			var n int
			_ = e.db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n)
			out.WriteString(table + "=" + strconv.Itoa(n) + " ")
		}
		var last time.Time
		_ = e.db.QueryRow(context.Background(), "SELECT max(last_used_at) FROM site_sessions").Scan(&last)
		return out.String() + last.String()
	}
	// Force the next use of the session to be due for a touch.
	if _, err := e.db.Exec(context.Background(), `UPDATE site_sessions SET last_used_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	before := snapshot()
	anon := []string{
		"/embed/v1/thread?site=" + e.site.PublicID + "&url=" + url.QueryEscape(blogPage),
		"/embed/v1/thread?site=" + e.site.PublicID + "&url=" + url.QueryEscape("https://blog.example/posts/never-commented"),
		"/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape(blogKey),
		"/api/v1/threads/comments?site=" + e.site.PublicID + "&key=" + url.QueryEscape("p:/nothing"),
		"/embed.js", "/avatars/generated/abcd.svg",
	}
	var docs []string
	for _, p := range anon {
		for _, cookies := range [][]*http.Cookie{nil, {ck, console}} {
			w := e.do(req{method: "GET", path: p, cookies: cookies, headers: map[string]string{"Authorization": "Bearer " + token}})
			if len(w.Result().Cookies()) > 0 {
				t.Errorf("%s set a cookie", p)
			}
			if strings.HasPrefix(p, "/embed/v1/thread") {
				docs = append(docs, w.Body.String())
			}
		}
	}
	if after := snapshot(); after != before {
		t.Errorf("anonymous reads changed the database:\n%s\n%s", before, after)
	}
	// The iframe document is the same with and without credentials, so
	// its height cannot depend on who reads (widget.md section 9).
	if docs[0] != docs[1] || docs[2] != docs[3] {
		t.Error("iframe document varies with credentials")
	}
}

func TestThreadDocumentModes(t *testing.T) {
	e := newEnv(t)
	doc := func(site, page string) (string, string, string) {
		w := e.do(req{method: "GET", path: "/embed/v1/thread?site=" + site + "&url=" + url.QueryEscape(page) + "&lang=en&theme=dark"})
		if w.Code != http.StatusOK {
			t.Fatalf("thread document = %d", w.Code)
		}
		body := w.Body.String()
		start := strings.Index(body, `id="kc-data">`) + len(`id="kc-data">`)
		var data map[string]any
		_ = json.Unmarshal([]byte(body[start:start+strings.Index(body[start:], "</script>")]), &data)
		mode, _ := data["mode"].(string)
		return mode, w.Header().Get("Content-Security-Policy"), w.Header().Get("Cache-Control")
	}
	mode, csp, cache := doc(e.site.PublicID, blogPage)
	if mode != modeNormal || !strings.Contains(csp, "frame-ancestors "+blogOrigin) || cache != "public, max-age=30" ||
		!strings.Contains(csp, "require-trusted-types-for 'script'") || !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("normal: %s / %s / %s", mode, csp, cache)
	}
	mode, csp, _ = doc(e.site.PublicID, "http://localhost:4000/posts/a")
	if mode != modePreview || !strings.Contains(csp, "frame-ancestors http://localhost:* http://127.0.0.1:*") || strings.Contains(csp, blogOrigin) {
		t.Errorf("preview: %s / %s", mode, csp)
	}
	mode, csp, _ = doc(e.site.PublicID, evilOrigin+"/x")
	if mode != modeForeign || !strings.Contains(csp, "frame-ancestors "+blogOrigin) {
		t.Errorf("foreign: %s / %s", mode, csp)
	}
	mode, csp, _ = doc("00000000-0000-4000-8000-000000000000", blogPage)
	if mode != modeInactive || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("unknown site: %s / %s", mode, csp)
	}
	if err := e.st.SuspendSite(t.Context(), e.site.ID, ""); err != nil {
		t.Fatal(err)
	}
	if mode, _, _ = doc(e.site.PublicID, blogPage); mode != modeClosed {
		t.Errorf("suspended: %s", mode)
	}
}

func TestBackchannelLogout(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	sid := e.idp.CurrentSID()
	console, _ := e.consoleLogin("ann") // a new identity session
	if w := e.do(req{method: "POST", path: "/auth/backchannel-logout", body: url.Values{"logout_token": {"garbage"}}}); w.Code != http.StatusBadRequest {
		t.Errorf("bad token = %d", w.Code)
	}
	w := e.do(req{method: "POST", path: "/auth/backchannel-logout", body: url.Values{"logout_token": {e.idp.LogoutToken("ann", sid)}}})
	if w.Code != http.StatusOK {
		t.Fatalf("logout = %d %s", w.Code, w.Body)
	}
	if decode(t, e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: bearer(token)}))["viewer"] != nil {
		t.Error("site session of the ended identity session survived")
	}
	// The console session came from a later identity session and stays.
	if w := e.do(req{method: "GET", path: "/api/v1/me", cookies: []*http.Cookie{console}}); w.Code != http.StatusOK {
		t.Errorf("console session of another identity session ended: %d", w.Code)
	}
}

func TestIDPWebhook(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	send := func(body string, sig string) int {
		return e.do(req{method: "POST", path: "/hooks/idp", body: body, headers: map[string]string{"Content-Type": "application/json", idp.SignatureHeader: sig}}).Code
	}
	deactivate := `{"aggregateID":"ann","aggregateType":"user","sequence":7,"event_type":"user.deactivated","created_at":"2026-10-10T00:00:00Z"}`
	if c := send(deactivate, idp.Sign([]byte(deactivate), "wrong", time.Now())); c != http.StatusUnauthorized {
		t.Errorf("bad signature = %d", c)
	}
	if c := send(deactivate, idp.Sign([]byte(deactivate), "hook-secret", time.Now().Add(-time.Hour))); c != http.StatusUnauthorized {
		t.Errorf("stale signature = %d", c)
	}
	if c := send(deactivate, idp.Sign([]byte(deactivate), "hook-secret", time.Now())); c != http.StatusOK {
		t.Fatalf("deactivate = %d", c)
	}
	u, err := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann")
	if err != nil || u.SuspendedAt == nil {
		t.Fatalf("not suspended: %+v %v", u, err)
	}
	if decode(t, e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: bearer(token)}))["viewer"] != nil {
		t.Error("session survived deactivation")
	}
	reactivate := `{"aggregateID":"ann","aggregateType":"user","sequence":8,"event_type":"user.reactivated","created_at":"2026-10-10T00:00:01Z"}`
	send(reactivate, idp.Sign([]byte(reactivate), "hook-secret", time.Now()))
	if u, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann"); u.SuspendedAt != nil {
		t.Error("reactivation did not lift the suspension")
	}
	// A replay of the deactivation is recorded already and does nothing.
	send(deactivate, idp.Sign([]byte(deactivate), "hook-secret", time.Now()))
	if u, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann"); u.SuspendedAt != nil {
		t.Error("replayed event applied twice")
	}
	removed := `{"aggregateID":"ann","aggregateType":"user","sequence":9,"event_type":"user.removed","created_at":"2026-10-10T00:00:02Z"}`
	if c := send(removed, idp.Sign([]byte(removed), "hook-secret", time.Now())); c != http.StatusOK {
		t.Fatalf("remove = %d", c)
	}
	if _, err := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account data survived removal: %v", err)
	}
}
