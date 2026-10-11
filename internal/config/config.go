// Package config reads the service configuration from COMMENTS_* environment
// variables (docs/design/architecture.md section 8). Product thresholds are
// rules in internal/policy, not settings.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/kite-plus/comments/internal/crypt"
)

// Config is the service configuration.
type Config struct {
	DatabaseURL    string
	HTTPAddr       string
	PublicURL      string
	TrustedProxies []string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	IDPWebhookSecret string
	IDPAPIToken      string
	IDPAPIURL        string

	DataKeys map[byte][]byte
	HMACKey  []byte
	IPKey    []byte

	SMTPURL  string
	MailFrom string
	LogLevel slog.Level

	// DevHTTPOrigins lets sites register and embed from http origins, for
	// the local test blogs. Refused when PublicURL is https.
	DevHTTPOrigins bool
	// AllowPrivateNetworks lets site verification and avatar fetches reach
	// private addresses and any port, for local development.
	AllowPrivateNetworks bool
}

// Load reads the configuration through getenv, usually os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL:      strings.TrimSpace(getenv("COMMENTS_DATABASE_URL")),
		HTTPAddr:         or(getenv("COMMENTS_HTTP_ADDR"), "127.0.0.1:8081"),
		PublicURL:        strings.TrimRight(or(getenv("COMMENTS_PUBLIC_URL"), "https://comments.kite.plus"), "/"),
		TrustedProxies:   list(getenv("COMMENTS_TRUSTED_PROXIES")),
		OIDCIssuer:       strings.TrimRight(or(getenv("COMMENTS_OIDC_ISSUER"), "https://id.kite.plus"), "/"),
		OIDCClientID:     strings.TrimSpace(getenv("COMMENTS_OIDC_CLIENT_ID")),
		OIDCClientSecret: strings.TrimSpace(getenv("COMMENTS_OIDC_CLIENT_SECRET")),
		IDPWebhookSecret: strings.TrimSpace(getenv("COMMENTS_IDP_WEBHOOK_SECRET")),
		IDPAPIToken:      strings.TrimSpace(getenv("COMMENTS_IDP_API_TOKEN")),
		SMTPURL:          strings.TrimSpace(getenv("COMMENTS_SMTP_URL")),
		MailFrom:         or(getenv("COMMENTS_MAIL_FROM"), "Kite Plus 评论 <notify@kite.plus>"),
		LogLevel:         slog.LevelInfo,
	}
	c.IDPAPIURL = strings.TrimRight(or(getenv("COMMENTS_IDP_API_URL"), c.OIDCIssuer), "/")

	pub, err := url.Parse(c.PublicURL)
	if err != nil || (pub.Scheme != "http" && pub.Scheme != "https") || pub.Host == "" || (pub.Path != "" && pub.Path != "/") {
		return Config{}, fmt.Errorf("COMMENTS_PUBLIC_URL: %q is not an http or https origin", c.PublicURL)
	}
	for name, v := range map[string]string{"COMMENTS_OIDC_ISSUER": c.OIDCIssuer, "COMMENTS_IDP_API_URL": c.IDPAPIURL} {
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("%s: %q is not an http or https URL", name, v)
		}
	}
	if v := strings.TrimSpace(getenv("COMMENTS_LOG_LEVEL")); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("COMMENTS_LOG_LEVEL: %q is not debug, info, warn or error", v)
		}
	}
	if c.DevHTTPOrigins, err = boolean(getenv, "COMMENTS_DEV_HTTP_ORIGINS"); err != nil {
		return Config{}, err
	}
	if c.DevHTTPOrigins && pub.Scheme == "https" {
		return Config{}, errors.New("COMMENTS_DEV_HTTP_ORIGINS is for local development only and cannot be used with an https COMMENTS_PUBLIC_URL")
	}
	if c.AllowPrivateNetworks, err = boolean(getenv, "COMMENTS_ALLOW_PRIVATE_NETWORKS"); err != nil {
		return Config{}, err
	}
	if v := strings.TrimSpace(getenv("COMMENTS_DATA_KEYS")); v != "" {
		if c.DataKeys, err = crypt.ParseDataKeys(v); err != nil {
			return Config{}, fmt.Errorf("COMMENTS_DATA_KEYS: %w", err)
		}
	}
	for name, dst := range map[string]*[]byte{"COMMENTS_HMAC_KEY": &c.HMACKey, "COMMENTS_IP_KEY": &c.IPKey} {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			if *dst, err = crypt.DecodeKey(v); err != nil {
				return Config{}, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if c.SMTPURL != "" {
		u, err := url.Parse(c.SMTPURL)
		if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Host == "" {
			// The value may carry a password, so it is not echoed.
			return Config{}, errors.New("COMMENTS_SMTP_URL is not an smtp:// or smtps:// URL")
		}
	}
	if _, err := mail.ParseAddress(c.MailFrom); err != nil {
		return Config{}, fmt.Errorf("COMMENTS_MAIL_FROM: %q is not a mail address", c.MailFrom)
	}
	return c, nil
}

// Secure reports whether the service runs on https, which decides cookie
// names and attributes.
func (c Config) Secure() bool { return strings.HasPrefix(c.PublicURL, "https://") }

// RequireDatabase fails when no database is configured.
func (c Config) RequireDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("COMMENTS_DATABASE_URL is not set")
	}
	return nil
}

// Keys builds the encryption and HMAC keys, which serve and worker need.
func (c Config) Keys() (*crypt.Keys, error) {
	var missing []string
	if len(c.DataKeys) == 0 {
		missing = append(missing, "COMMENTS_DATA_KEYS")
	}
	if c.HMACKey == nil {
		missing = append(missing, "COMMENTS_HMAC_KEY")
	}
	if c.IPKey == nil {
		missing = append(missing, "COMMENTS_IP_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s not set; see deploy/.env.example", strings.Join(missing, ", "))
	}
	return crypt.New(c.DataKeys, c.HMACKey, c.IPKey)
}

// RequireOIDC fails when the OIDC client is not configured.
func (c Config) RequireOIDC() error {
	if c.OIDCClientID == "" || c.OIDCClientSecret == "" {
		return errors.New("COMMENTS_OIDC_CLIENT_ID and COMMENTS_OIDC_CLIENT_SECRET must be set")
	}
	return nil
}

func boolean(getenv func(string) string, name string) (bool, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not true or false", name, v)
	}
	return b, nil
}

func list(v string) []string {
	var out []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func or(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return fallback
}
