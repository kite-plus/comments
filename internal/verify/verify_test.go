package verify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestPublic(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "::1", "fd00::1", "100.64.0.1", "0.0.0.0", "::ffff:127.0.0.1"} {
		if Public(netip.MustParseAddr(s)) {
			t.Errorf("%s counted as public", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "2606:4700::1111"} {
		if !Public(netip.MustParseAddr(s)) {
			t.Errorf("%s counted as private", s)
		}
	}
}

func TestPrivateAddressRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tok")) }))
	defer srv.Close()
	f := NewFetcher(false)
	if _, _, err := f.Get(context.Background(), srv.URL+"/", 100); err == nil || !errors.Is(err, ErrBlocked) {
		t.Errorf("fetch of a loopback server: %v", err)
	}
	f = NewFetcher(true)
	if body, _, err := f.Get(context.Background(), srv.URL+"/", 100); err != nil || string(body) != "tok" {
		t.Errorf("development fetch = %q, %v", body, err)
	}
	if _, _, err := f.Get(context.Background(), srv.URL+"/", 2); !errors.Is(err, ErrTooLarge) {
		t.Errorf("size limit: %v", err)
	}
}

func TestCheckMethods(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta name="Kite-Comments-Site" content=" meta-token "></head><body><meta name="kite-comments-site" content="body-token"></body>`))
	})
	mux.HandleFunc(FilePath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("file-token\n")) })
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := NewFetcher(true)
	f.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if name == "_kite-comments.127.0.0.1" {
			return []string{"kite-comments-site=dns-token", "other"}, nil
		}
		return nil, errors.New("nxdomain")
	}
	for token, method := range map[string]string{"dns-token": "dns", "meta-token": "meta", "file-token": "file"} {
		r := f.Check(context.Background(), srv.URL, func(s string) bool { return s == token })
		if !r.OK || r.Method != method {
			t.Errorf("%s: %+v", token, r)
		}
	}
	r := f.Check(context.Background(), srv.URL, func(s string) bool { return s == "body-token" })
	if r.OK || !strings.Contains(r.Detail, "does not match") {
		t.Errorf("a tag in the body counted: %+v", r)
	}
	if _, _, err := f.Get(context.Background(), srv.URL+"/away", 100); err == nil {
		t.Error("redirect to another host followed")
	}
}

// An owner sees why a check failed, never the address a name resolved to.
func TestCheckNamesNoResolvedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	f := NewFetcher(false)
	f.LookupTXT = func(context.Context, string) ([]string, error) { return nil, errors.New("nxdomain") }
	origin := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	r := f.Check(context.Background(), origin, func(string) bool { return true })
	if r.OK || strings.Contains(r.Detail, "127.0.0.1") || strings.Contains(r.Detail, "::1") || !strings.Contains(r.Detail, "public address") {
		t.Errorf("detail = %q", r.Detail)
	}
}
