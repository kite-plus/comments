// Package oidc is the service's OpenID Connect client: the authorization
// code flow with PKCE against the identity service, ID token validation and
// back-channel logout token validation (docs/design/login.md sections 4 and
// 6).
package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config names the identity service and this client.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// HTTPClient talks to the identity service; nil uses a client with a
	// ten second timeout.
	HTTPClient *http.Client
}

// Claims are the ID token claims the service uses.
type Claims struct {
	Issuer            string
	Subject           string
	SID               string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
	Picture           string
	Locale            string
}

// Logout is what a valid logout token names.
type Logout struct {
	Subject string
	SID     string
}

var ErrUnavailable = errors.New("identity service unavailable")

// Client is safe for concurrent use. Discovery runs on first use and is
// retried after a failure, so the service starts while the identity
// service is down.
type Client struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	provider *gooidc.Provider
	failedAt time.Time
}

// New returns a client; it does not contact the identity service.
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{cfg: cfg, http: hc}
}

func (c *Client) ctx(ctx context.Context) context.Context {
	return gooidc.ClientContext(ctx, c.http)
}

func (c *Client) discover(ctx context.Context) (*gooidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	if time.Since(c.failedAt) < 5*time.Second {
		return nil, ErrUnavailable
	}
	p, err := gooidc.NewProvider(c.ctx(ctx), c.cfg.Issuer)
	if err != nil {
		c.failedAt = time.Now()
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	c.provider = p
	return p, nil
}

func (c *Client) oauth(p *gooidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  c.cfg.RedirectURL,
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email"},
	}
}

// AuthURL returns where to send the browser to sign in.
func (c *Client) AuthURL(ctx context.Context, state, nonce, pkceVerifier string) (string, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	return c.oauth(p).AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(pkceVerifier)), nil
}

// Exchange trades an authorization code for a validated ID token: its
// signature, issuer, audience, expiry and nonce.
func (c *Client) Exchange(ctx context.Context, code, pkceVerifier, nonce string) (Claims, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	tok, err := c.oauth(p).Exchange(c.ctx(ctx), code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return Claims{}, fmt.Errorf("token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Claims{}, errors.New("token response has no id_token")
	}
	idt, err := p.Verifier(&gooidc.Config{ClientID: c.cfg.ClientID}).Verify(c.ctx(ctx), raw)
	if err != nil {
		return Claims{}, fmt.Errorf("id token: %w", err)
	}
	if idt.Nonce != nonce {
		return Claims{}, errors.New("id token: nonce does not match")
	}
	var cl struct {
		SID               string `json:"sid"`
		Email             string `json:"email"`
		EmailVerified     any    `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Picture           string `json:"picture"`
		Locale            string `json:"locale"`
	}
	if err := idt.Claims(&cl); err != nil {
		return Claims{}, fmt.Errorf("id token claims: %w", err)
	}
	return Claims{
		Issuer: idt.Issuer, Subject: idt.Subject, SID: cl.SID, Email: cl.Email, EmailVerified: truthy(cl.EmailVerified),
		Name: cl.Name, PreferredUsername: cl.PreferredUsername, Picture: cl.Picture, Locale: cl.Locale,
	}, nil
}

// Some providers send email_verified as a string.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	return false
}

const backchannelEvent = "http://schemas.openid.net/event/backchannel-logout"

// VerifyLogoutToken validates a back-channel logout token as OpenID
// Connect Back-Channel Logout 1.0 section 2.6 requires: signature, issuer,
// audience, a recent iat, the logout event, a sid or sub, and no nonce.
func (c *Client) VerifyLogoutToken(ctx context.Context, raw string) (Logout, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return Logout{}, err
	}
	// Expiry is checked below, since the token may omit it.
	t, err := p.Verifier(&gooidc.Config{ClientID: c.cfg.ClientID, SkipExpiryCheck: true}).Verify(c.ctx(ctx), raw)
	if err != nil {
		return Logout{}, fmt.Errorf("logout token: %w", err)
	}
	var cl struct {
		SID    string         `json:"sid"`
		Nonce  *string        `json:"nonce"`
		Events map[string]any `json:"events"`
		Iat    int64          `json:"iat"`
		Exp    int64          `json:"exp"`
	}
	if err := t.Claims(&cl); err != nil {
		return Logout{}, fmt.Errorf("logout token claims: %w", err)
	}
	now := time.Now()
	switch {
	case cl.Nonce != nil:
		return Logout{}, errors.New("logout token carries a nonce")
	case !hasEvent(cl.Events):
		return Logout{}, errors.New("logout token lacks the back-channel logout event")
	case cl.SID == "" && t.Subject == "":
		return Logout{}, errors.New("logout token names neither sid nor sub")
	case cl.Iat == 0 || now.Sub(time.Unix(cl.Iat, 0)).Abs() > 10*time.Minute:
		return Logout{}, errors.New("logout token is not recent")
	case cl.Exp != 0 && now.After(time.Unix(cl.Exp, 0).Add(time.Minute)):
		return Logout{}, errors.New("logout token expired")
	}
	return Logout{Subject: t.Subject, SID: cl.SID}, nil
}

func hasEvent(events map[string]any) bool {
	_, ok := events[backchannelEvent]
	return ok
}
