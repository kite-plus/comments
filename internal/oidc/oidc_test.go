package oidc_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/oidc/oidctest"
)

func setup(t *testing.T) (*oidctest.Provider, *oidc.Client) {
	t.Helper()
	p := oidctest.New("comments", "secret")
	t.Cleanup(p.Close)
	p.AddUser(oidctest.User{Subject: "u1", Name: "Alice", Email: "alice@example.com", EmailVerified: true, Locale: "zh-Hans"})
	c := oidc.New(oidc.Config{Issuer: p.Issuer, ClientID: "comments", ClientSecret: "secret", RedirectURL: "http://localhost:8081/auth/callback"})
	return p, c
}

func authorize(t *testing.T, authURL string) url.Values {
	t.Helper()
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	return loc.Query()
}

func TestCodeFlow(t *testing.T) {
	p, c := setup(t)
	sid := p.SignIn("u1")
	authURL, err := c.AuthURL(t.Context(), "state1", "nonce1", "verifier-verifier-verifier-verifier-1234")
	if err != nil {
		t.Fatal(err)
	}
	q := authorize(t, authURL)
	if q.Get("state") != "state1" {
		t.Fatalf("state = %q", q.Get("state"))
	}
	cl, err := c.Exchange(t.Context(), q.Get("code"), "verifier-verifier-verifier-verifier-1234", "nonce1")
	if err != nil {
		t.Fatal(err)
	}
	if cl.Subject != "u1" || cl.SID != sid || !cl.EmailVerified || cl.Name != "Alice" || cl.Issuer != p.Issuer {
		t.Errorf("claims = %+v", cl)
	}
}

func TestExchangeRejects(t *testing.T) {
	p, c := setup(t)
	p.SignIn("u1")
	authURL, _ := c.AuthURL(t.Context(), "s", "n", "verifier-verifier-verifier-verifier-1234")
	q := authorize(t, authURL)
	if _, err := c.Exchange(t.Context(), q.Get("code"), "another-verifier-another-verifier-12345", "n"); err == nil {
		t.Error("wrong PKCE verifier accepted")
	}
	q = authorize(t, authURL)
	if _, err := c.Exchange(t.Context(), q.Get("code"), "verifier-verifier-verifier-verifier-1234", "other"); err == nil {
		t.Error("wrong nonce accepted")
	}
}

func TestLogoutToken(t *testing.T) {
	p, c := setup(t)
	lo, err := c.VerifyLogoutToken(t.Context(), p.LogoutToken("u1", "sid-1"))
	if err != nil || lo.SID != "sid-1" || lo.Subject != "u1" {
		t.Fatalf("logout = %+v, %v", lo, err)
	}
	bad := map[string]map[string]any{
		"nonce":    {"iss": p.Issuer, "aud": "comments", "iat": time.Now().Unix(), "sid": "s", "nonce": "x", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}},
		"no event": {"iss": p.Issuer, "aud": "comments", "iat": time.Now().Unix(), "sid": "s"},
		"old":      {"iss": p.Issuer, "aud": "comments", "iat": time.Now().Add(-time.Hour).Unix(), "sid": "s", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}},
		"audience": {"iss": p.Issuer, "aud": "other", "iat": time.Now().Unix(), "sid": "s", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}},
		"no sid":   {"iss": p.Issuer, "aud": "comments", "iat": time.Now().Unix(), "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}},
	}
	for name, claims := range bad {
		if _, err := c.VerifyLogoutToken(t.Context(), p.Sign(claims)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	other := oidctest.New("comments", "secret")
	defer other.Close()
	forged := other.Sign(map[string]any{"iss": p.Issuer, "aud": "comments", "iat": time.Now().Unix(), "sid": "s",
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}})
	if _, err := c.VerifyLogoutToken(t.Context(), forged); err == nil || !strings.Contains(err.Error(), "logout token") {
		t.Errorf("token signed by another key: %v", err)
	}
}
