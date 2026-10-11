// Package oidctest is an in-process OpenID Connect provider for tests and
// the end-to-end harness: discovery, JWKS, an authorization endpoint that
// signs in a chosen user at once, a token endpoint that checks PKCE, and
// signed back-channel logout tokens. It is not a security boundary.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

// User is an account of the fake provider.
type User struct {
	Subject       string
	Name          string
	Username      string
	Email         string
	EmailVerified bool
	Locale        string
}

// Provider is a fake identity service.
type Provider struct {
	Issuer       string
	ClientID     string
	ClientSecret string

	key *rsa.PrivateKey
	srv *httptest.Server

	mu       sync.Mutex
	users    map[string]User
	current  string // subject signed in at the provider, "" for none
	sid      string // the provider session of the current user
	codes    map[string]grant
	Requests []url.Values // authorization requests seen, for assertions
	coop     bool
}

// SetCOOP makes the sign-in page send Cross-Origin-Opener-Policy:
// same-origin, which cuts a popup off from its opener, as some third-party
// login pages do (login.md section 4.2).
func (p *Provider) SetCOOP(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.coop = on
}

type grant struct {
	sub, nonce, challenge, redirect, sid string
	expires                              time.Time
}

// New starts a provider on a loopback port. Close it when done.
func New(clientID, clientSecret string) *Provider {
	p := newProvider(clientID, clientSecret)
	p.srv = httptest.NewServer(p.Handler())
	p.Issuer = p.srv.URL
	return p
}

// NewAt is New on a fixed issuer URL served by the caller with Handler.
func NewAt(issuer, clientID, clientSecret string) *Provider {
	p := newProvider(clientID, clientSecret)
	p.Issuer = strings.TrimRight(issuer, "/")
	return p
}

func newProvider(clientID, clientSecret string) *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &Provider{ClientID: clientID, ClientSecret: clientSecret, key: key,
		users: map[string]User{}, codes: map[string]grant{}}
}

// Close stops the server started by New.
func (p *Provider) Close() {
	if p.srv != nil {
		p.srv.Close()
	}
}

// AddUser registers an account.
func (p *Provider) AddUser(u User) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.users[u.Subject] = u
}

// SignIn makes subject the signed-in user with a new provider session and
// returns the session's sid. Authorization requests then return at once,
// as the real service does for a reader who is signed in.
func (p *Provider) SignIn(subject string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = subject
	p.sid = "sid-" + randomString()
	return p.sid
}

// SignOut ends the provider session.
func (p *Provider) SignOut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current, p.sid = "", ""
}

// Handler serves the provider's endpoints.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /authorize", p.chooseUser)
	mux.HandleFunc("POST /token", p.token)
	return mux
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/authorize",
		"token_endpoint":                        p.Issuer + "/token",
		"jwks_uri":                              p.Issuer + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"backchannel_logout_supported":          true,
		"backchannel_logout_session_supported":  true,
	})
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := p.key.PublicKey
	writeJSON(w, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "test", "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p.mu.Lock()
	p.Requests = append(p.Requests, q)
	current := p.current
	p.mu.Unlock()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	if current != "" {
		p.redirectWithCode(w, r, q)
		return
	}
	// No session: a minimal sign-in page listing the accounts.
	p.mu.Lock()
	if p.coop {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>Fake identity service</title><h1>Sign in</h1><form method="post">`)
	for k, v := range q {
		fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(k), html.EscapeString(v[0]))
	}
	for _, u := range p.users {
		fmt.Fprintf(&b, `<button name="subject" value="%s">%s</button>`, html.EscapeString(u.Subject), html.EscapeString(u.Name))
	}
	p.mu.Unlock()
	b.WriteString(`</form>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func (p *Provider) chooseUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	_, ok := p.users[r.PostForm.Get("subject")]
	p.mu.Unlock()
	if !ok {
		http.Error(w, "unknown user", http.StatusBadRequest)
		return
	}
	p.SignIn(r.PostForm.Get("subject"))
	p.redirectWithCode(w, r, r.PostForm)
}

func (p *Provider) redirectWithCode(w http.ResponseWriter, r *http.Request, q url.Values) {
	code := randomString()
	p.mu.Lock()
	p.codes[code] = grant{sub: p.current, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
		redirect: q.Get("redirect_uri"), sid: p.sid, expires: time.Now().Add(time.Minute)}
	p.mu.Unlock()
	u, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := u.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code)
	u := p.users[g.sub]
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || time.Now().After(g.expires) || g.redirect != r.PostForm.Get("redirect_uri") || b64(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer, "sub": u.Subject, "aud": p.ClientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": g.nonce, "sid": g.sid, "name": u.Name, "preferred_username": u.Username,
		"email": u.Email, "email_verified": u.EmailVerified, "locale": u.Locale,
	}
	writeJSON(w, map[string]any{"access_token": randomString(), "token_type": "Bearer", "expires_in": 3600,
		"id_token": p.Sign(claims)})
}

// LogoutToken returns a signed back-channel logout token for a provider
// session; empty values are left out.
func (p *Provider) LogoutToken(subject, sid string) string {
	claims := map[string]any{
		"iss": p.Issuer, "aud": p.ClientID, "iat": time.Now().Unix(), "exp": time.Now().Add(2 * time.Minute).Unix(),
		"jti":    randomString(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	}
	if subject != "" {
		claims["sub"] = subject
	}
	if sid != "" {
		claims["sid"] = sid
	}
	return p.Sign(claims)
}

// Sign makes an RS256 JWT with the provider's key.
func (p *Provider) Sign(claims map[string]any) string {
	header := b64(mustJSON(map[string]string{"alg": "RS256", "kid": "test", "typ": "JWT"}))
	body := b64(mustJSON(claims))
	sum := sha256.Sum256([]byte(header + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return header + "." + body + "." + b64(sig)
}

// CurrentSID is the sid of the provider session, "" when signed out.
func (p *Provider) CurrentSID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sid
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return b64(b[:])
}
