// Command harness runs everything the browser tests need on loopback
// ports: a fake identity service, the comment service behind a stand-in for
// the reverse proxy (the cookie rules of
// deploy/edge/comments-routes.caddyfile), the two test blogs and an
// unrelated site. It prints its addresses as one JSON line and serves until
// stdin closes. It needs COMMENTS_TEST_DATABASE_URL and the built widget
// (make web).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kite-plus/comments/internal/api"
	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/devblogs"
	"github.com/kite-plus/comments/internal/idp/idptest"
	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/oidc/oidctest"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/store/storetest"
	"github.com/kite-plus/comments/internal/verify"
	"github.com/kite-plus/comments/internal/worker"
	"github.com/kite-plus/comments/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		os.Exit(1)
	}
}

func listen() (net.Listener, int) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	return l, l.Addr().(*net.TCPAddr).Port
}

func run() error {
	ctx := context.Background()
	dbURL := os.Getenv("COMMENTS_TEST_DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("COMMENTS_TEST_DATABASE_URL is not set")
	}
	st, schema, drop, err := storetest.Open(ctx, dbURL, "e2e_")
	if err != nil {
		return err
	}
	defer drop()
	defer st.Close()

	idpL, idpPort := listen()
	pubL, pubPort := listen()
	aL, aPort := listen()
	bL, bPort := listen()
	evilL, evilPort := listen()
	issuer := fmt.Sprintf("http://localhost:%d", idpPort)
	public := fmt.Sprintf("http://localhost:%d", pubPort)
	blogA := fmt.Sprintf("http://blog-a.localhost:%d", aPort)
	blogB := fmt.Sprintf("http://blog-b.localhost:%d", bPort)
	evil := fmt.Sprintf("http://evil.localhost:%d", evilPort)

	p := oidctest.NewAt(issuer, "comments", "secret")
	p.AddUser(oidctest.User{Subject: "ann", Name: "Ann", Username: "ann", Email: "ann@example.com", EmailVerified: true, Locale: "zh-Hans"})
	p.AddUser(oidctest.User{Subject: "bob", Name: "Bob", Username: "bob", Email: "bob@example.com", EmailVerified: true, Locale: "en"})
	go func() { _ = http.Serve(idpL, p.Handler()) }()

	for _, x := range []struct{ id, name, origin string }{
		{devblogs.SiteA, "Blog A", blogA},
		{devblogs.SiteB, "Blog B", blogB},
	} {
		if _, err := st.CreateSite(ctx, store.NewSite{Name: x.name, Origins: []string{x.origin}, Verified: true, PublicID: x.id}); err != nil {
			return err
		}
	}

	keys, err := testKeys()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// Events are checked against the identity service, faked here.
	identity := idptest.New()
	defer identity.Close()
	srv := &api.Server{
		Store: st, Keys: keys, PublicURL: public, Issuer: issuer, IDPWebhookSecret: "hook-secret", IDP: identity.Client(), DevHTTPOrigins: true,
		OIDC:    oidc.New(oidc.Config{Issuer: issuer, ClientID: "comments", ClientSecret: "secret", RedirectURL: public + "/auth/callback"}),
		Fetcher: verify.NewFetcher(true), Widget: web.Widget(), Console: web.Console(), Log: log,
	}
	h, err := srv.Handler()
	if err != nil {
		return err
	}
	rec := &recorder{}
	box := &mailbox{}
	w := &worker.Worker{Store: st, Keys: keys, Mailer: box, PublicURL: public, Issuer: issuer, Log: log}
	loop, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-loop.Done():
				return
			case <-t.C:
				w.SendMail(loop)
				w.Minutely(loop)
			}
		}
	}()
	defer func() {
		stop()
		<-done
	}()

	control := http.NewServeMux()
	control.HandleFunc("GET /__e2e/seen", func(rw http.ResponseWriter, _ *http.Request) { writeJSON(rw, rec.list()) })
	control.HandleFunc("POST /__e2e/seen/reset", func(rw http.ResponseWriter, _ *http.Request) { rec.reset() })
	control.HandleFunc("GET /__e2e/mail", func(rw http.ResponseWriter, _ *http.Request) { writeJSON(rw, box.list()) })
	// Holds a site's member comments for review, or stops doing so.
	control.HandleFunc("POST /__e2e/review", func(rw http.ResponseWriter, r *http.Request) {
		site, err := st.Site(r.Context(), r.URL.Query().Get("site"))
		if err != nil {
			http.Error(rw, err.Error(), 404)
			return
		}
		policy := model.MemberPublish
		if r.URL.Query().Get("on") == "1" {
			policy = model.MemberReview
		}
		if _, err := st.UpdateSite(r.Context(), site.ID, store.SiteSettings{MemberPolicy: &policy}); err != nil {
			http.Error(rw, err.Error(), 500)
		}
	})
	control.HandleFunc("POST /__e2e/idp/signout", func(rw http.ResponseWriter, _ *http.Request) { p.SignOut() })
	control.HandleFunc("POST /__e2e/idp/coop", func(rw http.ResponseWriter, r *http.Request) { p.SetCOOP(r.URL.Query().Get("on") == "1") })
	// The identity service ends its session: back-channel logout with the
	// session's sid, then the session is gone.
	control.HandleFunc("POST /__e2e/idp/logout", func(rw http.ResponseWriter, r *http.Request) {
		sid := p.CurrentSID()
		body := "logout_token=" + p.LogoutToken(r.URL.Query().Get("sub"), sid)
		req := httpRequest("POST", public+"/auth/backchannel-logout", body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		_ = resp.Body.Close()
		p.SignOut()
		writeJSON(rw, map[string]any{"status": resp.StatusCode, "sid": sid})
	})
	control.Handle("/", proxy(h, rec))
	go func() { _ = http.Serve(pubL, control) }()
	go func() { _ = http.Serve(aL, devblogs.A(public).Handler()) }()
	go func() { _ = http.Serve(bL, devblogs.B(public).Handler()) }()
	go func() { _ = http.Serve(evilL, evilSite(public)) }()

	out, _ := json.Marshal(map[string]any{"public": public, "issuer": issuer, "blogA": blogA, "blogB": blogB, "evil": evil,
		"siteA": devblogs.SiteA, "siteB": devblogs.SiteB, "schema": schema})
	fmt.Println(string(out))
	// Serve until the test runner closes stdin.
	_, _ = io.Copy(io.Discard, bufio.NewReader(os.Stdin))
	return nil
}

// anonymous are the routes deploy/edge/comments-routes.caddyfile strips the
// Cookie header on.
var anonymous = regexp.MustCompile(`^(/embed\.js|/embed/.*|/static/widget/.*|/avatars/.*|/api/v1/threads/comments|/api/v1/embed-info|/api/v1/comments/[0-9]+/replies)$`)

// proxy stands in for the reverse proxy: on anonymous GETs it removes the
// Cookie header as the Caddyfile does, and it records what reached the
// service.
func proxy(next http.Handler, rec *recorder) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sent := r.Header.Get("Cookie") != ""
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && anonymous.MatchString(r.URL.Path) {
			r.Header.Del("Cookie")
		}
		sr := &statusRecorder{ResponseWriter: rw, status: 200}
		next.ServeHTTP(sr, r)
		rec.add(seen{Method: r.Method, Path: r.URL.Path, BrowserSentCookie: sent, ServiceGotCookie: r.Header.Get("Cookie") != "",
			SecFetchSite: r.Header.Get("Sec-Fetch-Site"), Origin: r.Header.Get("Origin"), Status: sr.status,
			SetCookie: len(sr.Header().Values("Set-Cookie")) > 0})
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

type seen struct {
	Method            string `json:"method"`
	Path              string `json:"path"`
	BrowserSentCookie bool   `json:"browser_sent_cookie"`
	ServiceGotCookie  bool   `json:"service_got_cookie"`
	SecFetchSite      string `json:"sec_fetch_site"`
	Origin            string `json:"origin"`
	Status            int    `json:"status"`
	SetCookie         bool   `json:"set_cookie"`
}

type recorder struct {
	mu   sync.Mutex
	seen []seen
}

func (r *recorder) add(s seen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, s)
}

func (r *recorder) list() []seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seen{}, r.seen...)
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = nil
}

type mailbox struct {
	mu   sync.Mutex
	mail []mailer.Message
}

func (m *mailbox) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mail = append(m.mail, msg)
	return nil
}

func (m *mailbox) list() []mailer.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mailer.Message{}, m.mail...)
}

// evilSite is a site that is not registered: it embeds blog A's widget
// (roadmap T7) and runs a host page's attacks (T8).
func evilSite(public string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /embed", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(rw, `<!doctype html><html lang="en"><body><h1>Not blog A</h1>
<kite-comments site="%s" lazy="false"></kite-comments><script src="%s/embed.js" async></script></body></html>`, devblogs.SiteA, public)
	})
	return mux
}

func testKeys() (*crypt.Keys, error) {
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	d, err := crypt.ParseDataKeys("1:" + k)
	if err != nil {
		return nil, err
	}
	return crypt.New(d, bytes.Repeat([]byte{8}, 32), bytes.Repeat([]byte{9}, 32))
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

func httpRequest(method, url, body string) *http.Request {
	r, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	return r
}
