package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/oidc/oidctest"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/store/storetest"
	"github.com/kite-plus/comments/internal/verify"
)

const (
	testPublic = "http://comments.test"
	blogOrigin = "https://blog.example"
	blogPage   = "https://blog.example/posts/a/"
	blogKey    = "p:/posts/a"
	evilOrigin = "https://evil.example"
)

type env struct {
	db    *pgx.Conn
	t     *testing.T
	srv   *Server
	h     http.Handler
	idp   *oidctest.Provider
	st    *store.Store
	keys  *crypt.Keys
	site  model.Site
	other model.Site
}

func testKeys(t *testing.T) *crypt.Keys {
	t.Helper()
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	d, err := crypt.ParseDataKeys("1:" + k)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.New(d, bytes.Repeat([]byte{8}, 32), bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, schema := storetest.NewWithSchema(t)
	p := oidctest.New("comments", "secret")
	t.Cleanup(p.Close)
	for _, u := range []oidctest.User{
		{Subject: "ann", Name: "Ann", Email: "ann@example.com", EmailVerified: true, Locale: "zh-Hans"},
		{Subject: "bob", Name: "Bob", Email: "bob@example.com", EmailVerified: true, Locale: "en"},
		{Subject: "owner", Name: "Owner", Email: "owner@example.com", EmailVerified: true},
	} {
		p.AddUser(u)
	}
	keys := testKeys(t)
	e := &env{t: t, idp: p, st: st, keys: keys, db: storetest.Conn(t, schema)}
	e.srv = &Server{
		Store: st, Keys: keys, PublicURL: testPublic, Issuer: p.Issuer, IDPWebhookSecret: "hook-secret",
		OIDC:    oidc.New(oidc.Config{Issuer: p.Issuer, ClientID: "comments", ClientSecret: "secret", RedirectURL: testPublic + "/auth/callback"}),
		Fetcher: verify.NewFetcher(true),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h, err := e.srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	ctx := t.Context()
	if e.site, err = st.CreateSite(ctx, store.NewSite{Name: "Blog", Origins: []string{blogOrigin}, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if e.other, err = st.CreateSite(ctx, store.NewSite{Name: "Other", Origins: []string{"https://other.example"}, Verified: true}); err != nil {
		t.Fatal(err)
	}
	return e
}

type req struct {
	method  string
	path    string
	body    any // marshaled as JSON unless a string or url.Values
	headers map[string]string
	cookies []*http.Cookie
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	ct := ""
	switch b := r.body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	case url.Values:
		body, ct = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, _ := json.Marshal(b)
		body, ct = bytes.NewReader(raw), "application/json"
	}
	hr := httptest.NewRequest(r.method, r.path, body)
	if ct != "" {
		hr.Header.Set("Content-Type", ct)
	}
	for k, v := range r.headers {
		if v == "" {
			hr.Header.Del(k)
		} else {
			hr.Header.Set(k, v)
		}
	}
	for _, c := range r.cookies {
		hr.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, hr)
	return w
}

// own are the headers a fetch from the service's own pages carries.
func own() map[string]string {
	return map[string]string{"Origin": testPublic, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}
}

func with(h map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
	}
	return m
}

func verifierPair() (verifier, challenge string) {
	verifier = crypt.Token()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize follows the authorization URL at the fake identity service and
// returns the callback path it sends the browser to.
func (e *env) authorize(authURL string) string {
	e.t.Helper()
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(authURL)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, testPublic+"/auth/callback?") {
		e.t.Fatalf("authorize redirected to %q (%d)", loc, resp.StatusCode)
	}
	return strings.TrimPrefix(loc, testPublic)
}

// popupLogin runs the popup flow for a reader on a site and returns the
// site session token and cookie.
func (e *env) popupLogin(sub string, site model.Site) (string, *http.Cookie) {
	e.t.Helper()
	e.idp.SignIn(sub)
	verifier, challenge := verifierPair()
	w := e.do(req{method: "POST", path: "/auth/transactions", body: map[string]string{"site": site.PublicID, "challenge": challenge}, headers: own()})
	if w.Code != http.StatusOK {
		e.t.Fatalf("transaction = %d %s", w.Code, w.Body)
	}
	binder := cookie(w, "comments_binder")
	cb := e.authorize(decode(e.t, w)["authorize_url"].(string))
	w = e.do(req{method: "GET", path: cb, cookies: []*http.Cookie{binder}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-step="done"`) {
		e.t.Fatalf("callback = %d %s", w.Code, w.Body)
	}
	w = e.do(req{method: "POST", path: "/api/v1/widget/sessions", body: map[string]string{"site": site.PublicID, "verifier": verifier}, headers: own()})
	if w.Code != http.StatusCreated {
		e.t.Fatalf("redeem = %d %s", w.Code, w.Body)
	}
	token := decode(e.t, w)["token"].(string)
	return token, cookie(w, e.srv.siteCookieName(site.PublicID))
}

func bearer(token string) map[string]string {
	return with(own(), "Authorization", "Bearer "+token)
}

// consoleLogin signs a reader in to the reader center and returns the
// session cookie and CSRF token.
func (e *env) consoleLogin(sub string) (*http.Cookie, string) {
	e.t.Helper()
	e.idp.SignIn(sub)
	w := e.do(req{method: "GET", path: "/auth/login?return=/me"})
	if w.Code != http.StatusFound {
		e.t.Fatalf("console login = %d", w.Code)
	}
	binder := cookie(w, "comments_binder")
	w = e.do(req{method: "GET", path: e.authorize(w.Header().Get("Location")), cookies: []*http.Cookie{binder}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/me" {
		e.t.Fatalf("console callback = %d %s", w.Code, w.Header().Get("Location"))
	}
	session := cookie(w, "comments_session")
	w = e.do(req{method: "GET", path: "/api/v1/me", cookies: []*http.Cookie{session}})
	if w.Code != http.StatusOK {
		e.t.Fatalf("me = %d %s", w.Code, w.Body)
	}
	return session, decode(e.t, w)["csrf_token"].(string)
}

func (e *env) post(token string, site model.Site, body string, extra map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	in := map[string]any{"site": site.PublicID, "key": blogKey, "url": blogPage, "title": "Post A", "body": body}
	for k, v := range extra {
		in[k] = v
	}
	return e.do(req{method: "POST", path: "/api/v1/comments", body: in, headers: bearer(token)})
}
