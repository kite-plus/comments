package config

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var testKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != "127.0.0.1:8081" || c.PublicURL != "https://comments.kite.plus" || c.OIDCIssuer != "https://id.kite.plus" ||
		c.IDPAPIURL != c.OIDCIssuer || c.LogLevel != slog.LevelInfo || !c.Secure() || c.DevHTTPOrigins {
		t.Errorf("defaults = %+v", c)
	}
	if c.RequireDatabase() == nil || c.RequireOIDC() == nil {
		t.Error("requirements passed without values")
	}
	if _, err := c.Keys(); err == nil || !strings.Contains(err.Error(), "COMMENTS_DATA_KEYS") {
		t.Errorf("Keys without keys: %v", err)
	}
}

func TestValues(t *testing.T) {
	c, err := Load(env(map[string]string{
		"COMMENTS_DATABASE_URL":     "postgres://x",
		"COMMENTS_PUBLIC_URL":       "http://localhost:8081/",
		"COMMENTS_OIDC_ISSUER":      "http://localhost:8090",
		"COMMENTS_DATA_KEYS":        "1:" + testKey,
		"COMMENTS_HMAC_KEY":         testKey,
		"COMMENTS_IP_KEY":           testKey,
		"COMMENTS_SMTP_URL":         "smtp://localhost:1025",
		"COMMENTS_DEV_HTTP_ORIGINS": "true",
		"COMMENTS_LOG_LEVEL":        "debug",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL != "http://localhost:8081" || c.Secure() || !c.DevHTTPOrigins || c.LogLevel != slog.LevelDebug {
		t.Errorf("config = %+v", c)
	}
	if _, err := c.Keys(); err != nil {
		t.Error(err)
	}
}

func TestInvalidValues(t *testing.T) {
	cases := []map[string]string{
		{"COMMENTS_PUBLIC_URL": "comments.kite.plus"},
		{"COMMENTS_PUBLIC_URL": "https://comments.kite.plus/sub"},
		{"COMMENTS_OIDC_ISSUER": "id.kite.plus"},
		{"COMMENTS_LOG_LEVEL": "loud"},
		{"COMMENTS_DATA_KEYS": "1:short"},
		{"COMMENTS_HMAC_KEY": "short"},
		{"COMMENTS_SMTP_URL": "http://mail"},
		{"COMMENTS_MAIL_FROM": "not an address"},
		{"COMMENTS_DEV_HTTP_ORIGINS": "maybe"},
		// The development setting must never reach an https deployment.
		{"COMMENTS_DEV_HTTP_ORIGINS": "true"},
		{"COMMENTS_DEV_HTTP_ORIGINS": "true", "COMMENTS_PUBLIC_URL": "https://comments.example"},
		{"COMMENTS_ALLOW_PRIVATE_NETWORKS": "true"},
		{"COMMENTS_ALLOW_PRIVATE_NETWORKS": "true", "COMMENTS_PUBLIC_URL": "https://comments.example"},
	}
	for _, m := range cases {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%v accepted", m)
		}
	}
}
