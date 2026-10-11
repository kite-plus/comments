package pagekey

import (
	"strings"
	"testing"
)

func TestFromURL(t *testing.T) {
	cases := []struct {
		url, key, origin string
		keys             []string
	}{
		// The table in widget.md section 5.
		{"https://blog.example.com/posts/hello/", "p:/posts/hello", "https://blog.example.com", nil},
		{"https://www.blog.example.com/posts/hello.html?utm_source=x#top", "p:/posts/hello", "https://www.blog.example.com", nil},
		{"https://blog.example.com/?p=123", "p:/?p=123", "https://blog.example.com", []string{"p"}},
		// Rule 4.
		{"https://a.example/posts/%E4%BD%A0%E5%A5%BD", "p:/posts/你好", "https://a.example", nil},
		{"https://a.example/posts/你好", "p:/posts/你好", "https://a.example", nil},
		{"https://a.example/a%2Fb%3f%25", "p:/a%2Fb%3F%25", "https://a.example", nil},
		{"https://a.example/a/./b/../c", "p:/a/c", "https://a.example", nil},
		{"https://a.example/posts/x/index.html", "p:/posts/x", "https://a.example", nil},
		{"https://a.example/index.htm", "p:/", "https://a.example", nil},
		{"https://a.example/a.htm", "p:/a", "https://a.example", nil},
		{"https://a.example/", "p:/", "https://a.example", nil},
		{"https://a.example", "p:/", "https://a.example", nil},
		{"https://a.example/Case/Kept", "p:/Case/Kept", "https://a.example", nil},
		{"https://A.Example:443/x", "p:/x", "https://a.example", nil},
		{"http://blog-a.localhost:8301/x/", "p:/x", "http://blog-a.localhost:8301", nil},
		{"https://bücher.example/x", "p:/x", "https://xn--bcher-kva.example", nil},
		// Rule 5: kept keys sorted, others dropped.
		{"https://a.example/?b=2&a=1&c=3", "p:/?a=1&b=2", "https://a.example", []string{"b", "a"}},
		{"https://a.example/?utm=1", "p:/", "https://a.example", []string{"p"}},
		// Invalid escapes and control characters stay encoded.
		{"https://a.example/%FF%00x", "p:/%FF%00x", "https://a.example", nil},
	}
	for _, c := range cases {
		key, origin, err := FromURL(c.url, c.keys)
		if err != nil || key != c.key || origin != c.origin {
			t.Errorf("FromURL(%q) = %q, %q, %v; want %q, %q", c.url, key, origin, err, c.key, c.origin)
		}
	}
}

func TestLongPathHashes(t *testing.T) {
	key, _, err := FromURL("https://a.example/"+strings.Repeat("x", 600), nil)
	if err != nil || !strings.HasPrefix(key, "h:") || len(key) != 66 || !Valid(key) {
		t.Fatalf("long key = %q, %v", key, err)
	}
}

func TestBadURLs(t *testing.T) {
	for _, raw := range []string{"", "/relative", "ftp://a.example/x", "https://user@a.example/", "javascript:alert(1)"} {
		if _, _, err := FromURL(raw, nil); err == nil {
			t.Errorf("FromURL(%q) accepted", raw)
		}
	}
}

func TestExplicit(t *testing.T) {
	if k, err := Explicit("  01J9Z3K6V4R8Q2X5N7M1B0C3D4 "); err != nil || k != "k:01J9Z3K6V4R8Q2X5N7M1B0C3D4" {
		t.Errorf("Explicit = %q, %v", k, err)
	}
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("x", 201)} {
		if _, err := Explicit(bad); err == nil {
			t.Errorf("Explicit(%q) accepted", bad)
		}
	}
	if _, err := Explicit(strings.Repeat("字", 200)); err != nil {
		t.Errorf("200 characters rejected: %v", err)
	}
}

func TestValid(t *testing.T) {
	for _, k := range []string{"k:wp:12", "p:/", "p:/a?p=1"} {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false", k)
		}
	}
	for _, k := range []string{"", "x:1", "k: x", "p:a", "h:zz", "k:"} {
		if Valid(k) {
			t.Errorf("Valid(%q) = true", k)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	good := map[string]string{
		"https://Blog.Example.com":      "https://blog.example.com",
		"https://blog.example.com/":     "https://blog.example.com",
		"https://blog.example.com:443":  "https://blog.example.com",
		"https://blog.example.com:8443": "https://blog.example.com:8443",
		"http://localhost:8301":         "http://localhost:8301",
	}
	for in, want := range good {
		if got, err := NormalizeOrigin(in); err != nil || got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"blog.example.com", "https://blog.example.com/path", "https://a.example/?q", "https://u@a.example", "ftp://a.example"} {
		if _, err := NormalizeOrigin(bad); err == nil {
			t.Errorf("NormalizeOrigin(%q) accepted", bad)
		}
	}
}
