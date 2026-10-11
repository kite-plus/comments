// Package roundtrip holds the export and import round trip of
// docs/design/sites.md section 7: export a site, import it into an empty
// instance, and both must hold the same threads and comments. The only
// difference allowed is that readers' comments come in as external authors.
package roundtrip

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/api"
	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/oidc/oidctest"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/store/storetest"
	"github.com/kite-plus/comments/internal/verify"
	"github.com/kite-plus/comments/internal/worker"
)

const public = "http://comments.test"

type instance struct {
	t       *testing.T
	st      *store.Store
	keys    *crypt.Keys
	h       http.Handler
	idp     *oidctest.Provider
	site    model.Site
	session *http.Cookie
	csrf    string
}

func keys(t *testing.T) *crypt.Keys {
	k := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	d, _ := crypt.ParseDataKeys("1:" + k)
	ks, err := crypt.New(d, bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// newInstance is an empty service with one site whose owner is signed in
// to the console.
func newInstance(t *testing.T) *instance {
	t.Helper()
	in := &instance{t: t, st: storetest.New(t), keys: keys(t), idp: oidctest.New("comments", "secret")}
	t.Cleanup(in.idp.Close)
	in.idp.AddUser(oidctest.User{Subject: "owner", Name: "Owner", Email: "owner@example.com", EmailVerified: true})
	srv := &api.Server{Store: in.st, Keys: in.keys, PublicURL: public, Issuer: in.idp.Issuer, Fetcher: verify.NewFetcher(false),
		OIDC: oidc.New(oidc.Config{Issuer: in.idp.Issuer, ClientID: "comments", ClientSecret: "secret", RedirectURL: public + "/auth/callback"}),
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil))}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	in.h = h
	in.login()
	owner, err := in.st.UserBySubject(t.Context(), in.idp.Issuer, "owner")
	if err != nil {
		t.Fatal(err)
	}
	in.site, err = in.st.CreateSite(t.Context(), store.NewSite{Name: "Blog", Origins: []string{"https://blog.example"}, Verified: true, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (in *instance) do(method, path, ct string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	if in.session != nil {
		r.AddCookie(in.session)
		r.Header.Set("X-CSRF-Token", in.csrf)
	}
	w := httptest.NewRecorder()
	in.h.ServeHTTP(w, r)
	return w
}

func (in *instance) login() {
	in.idp.SignIn("owner")
	w := in.do("GET", "/auth/login?return=/me", "", nil)
	var binder *http.Cookie
	for _, c := range w.Result().Cookies() {
		binder = c
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Get(w.Header().Get("Location"))
	if err != nil {
		in.t.Fatal(err)
	}
	_ = resp.Body.Close()
	r := httptest.NewRequest("GET", strings.TrimPrefix(resp.Header.Get("Location"), public), nil)
	r.AddCookie(binder)
	w = httptest.NewRecorder()
	in.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == "comments_session" {
			in.session = c
		}
	}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(in.do("GET", "/api/v1/me", "", nil).Body.Bytes(), &me)
	in.csrf = me.CSRF
}

func (in *instance) export() map[string]any {
	w := in.do("GET", "/api/v1/sites/"+in.site.PublicID+"/export?format=json", "", nil)
	if w.Code != http.StatusOK {
		in.t.Fatalf("export = %d %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		in.t.Fatal(err)
	}
	return out
}

func seed(t *testing.T, in *instance) {
	ctx := t.Context()
	reader, _, err := in.st.LoginUser(ctx, store.OIDCLogin{Issuer: "https://id.example", Subject: "r", DisplayName: "Reader", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	mk := func(key string, parent *int64, n store.NewComment) model.Comment {
		page := "https://blog.example" + strings.TrimPrefix(key, "p:")
		if strings.HasPrefix(key, "k:") {
			page = "https://blog.example/posts/keyed"
		}
		n.SiteID, n.ThreadKey, n.ThreadURL, n.ThreadTitle, n.PagePublished, n.ParentID = in.site.ID, key, page, "T "+key, &published, parent
		n.RenderVersion = 1
		n.BodyHTML = "<p>" + n.BodyMD + "</p>"
		c, err := in.st.CreateComment(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	user := store.NewComment{AuthorKind: model.AuthorUser, AuthorUserID: &reader.ID, Status: model.StatusPublished}
	guest := func(body, status string) store.NewComment {
		return store.NewComment{AuthorKind: model.AuthorGuest, AuthorName: "Gail", AuthorURL: "https://gail.example",
			AuthorEmailEnc: in.keys.Encrypt("gail@example.com"), AuthorEmailHMAC: in.keys.EmailHMAC("gail@example.com"), BodyMD: body, Status: status}
	}
	u := user
	u.BodyMD = "a reader's comment"
	top := mk("p:/a", nil, u)
	g := mk("p:/a", &top.ID, guest("a guest's reply", model.StatusPublished))
	r2 := user
	r2.BodyMD = "a reply to the guest"
	mk("p:/a", &g.ID, r2)
	mk("p:/a", nil, guest("held for review", model.StatusPending))
	mk("p:/a", nil, guest("junk", model.StatusSpam))
	pinned := mk("p:/b", nil, guest("pinned", model.StatusPublished))
	if _, err := in.st.Moderate(ctx, store.ModerateIn{SiteID: in.site.ID, CommentID: pinned.ID, Action: "pin"}); err != nil {
		t.Fatal(err)
	}
	gone := mk("p:/b", nil, guest("deleted later", model.StatusPublished))
	if _, err := in.st.Moderate(ctx, store.ModerateIn{SiteID: in.site.ID, CommentID: gone.ID, Action: "hide"}); err != nil {
		t.Fatal(err)
	}
	mk("k:post-1", nil, guest("on a keyed page", model.StatusPublished))
	thread, _ := in.st.ThreadByKey(ctx, in.site.ID, "p:/b")
	if err := in.st.LockThread(ctx, in.site.ID, thread.ID, true, nil); err != nil {
		t.Fatal(err)
	}
}

// normalize keeps what must survive the trip, keyed by body.
func normalize(export map[string]any) (threads map[string]string, comments map[string]string) {
	threads = map[string]string{}
	keyOf := map[string]string{}
	for _, x := range export["threads"].([]any) {
		th := x.(map[string]any)
		keyOf[th["id"].(string)] = th["key"].(string)
		b, _ := json.Marshal([]any{th["url"], th["title"], th["locked"], th["published_at"]})
		threads[th["key"].(string)] = string(b)
	}
	bodyOf := map[string]string{}
	for _, x := range export["comments"].([]any) {
		c := x.(map[string]any)
		bodyOf[c["id"].(string)] = c["body"].(map[string]any)["source"].(string)
	}
	comments = map[string]string{}
	for _, x := range export["comments"].([]any) {
		c := x.(map[string]any)
		a := c["author"].(map[string]any)
		kind := a["kind"]
		if kind == "user" {
			kind = "external"
		}
		parent := ""
		if p, ok := c["parent_id"].(string); ok {
			parent = bodyOf[p]
		}
		root := ""
		if p, ok := c["root_id"].(string); ok {
			root = bodyOf[p]
		}
		b, _ := json.Marshal([]any{keyOf[c["thread_id"].(string)], c["status"], c["pinned"], c["created_at"], parent, root,
			kind, a["name"], a["url"], a["email"]})
		comments[c["body"].(map[string]any)["source"].(string)] = string(b)
	}
	return threads, comments
}

func TestExportImportRoundTrip(t *testing.T) {
	src := newInstance(t)
	seed(t, src)
	exported := src.export()

	dst := newInstance(t)
	raw, _ := json.Marshal(exported)
	w := dst.upload(raw)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", w.Code, w.Body)
	}
	var im struct {
		ID    string `json:"id"`
		Stats struct {
			Preview struct {
				Total     int `json:"total"`
				Threads   int `json:"threads"`
				WithEmail int `json:"with_email"`
			} `json:"preview"`
		} `json:"stats"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &im)
	if im.Stats.Preview.Total != 8 || im.Stats.Preview.Threads != 3 || im.Stats.Preview.WithEmail != 6 {
		t.Errorf("preview = %+v", im.Stats.Preview)
	}
	if w := dst.do("POST", "/api/v1/sites/"+dst.site.PublicID+"/imports/"+im.ID+"/run", "", nil); w.Code != http.StatusAccepted {
		t.Fatalf("run = %d %s", w.Code, w.Body)
	}
	wk := &worker.Worker{Store: dst.st, Keys: dst.keys, PublicURL: public, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	wk.Minutely(context.Background())
	// A second run of the same file adds nothing.
	w = dst.upload(raw)
	_ = json.Unmarshal(w.Body.Bytes(), &im)
	dst.do("POST", "/api/v1/sites/"+dst.site.PublicID+"/imports/"+im.ID+"/run", "", nil)
	wk.Minutely(context.Background())

	wantThreads, wantComments := normalize(exported)
	gotThreads, gotComments := normalize(dst.export())
	if len(gotComments) != len(wantComments) || len(dst.export()["comments"].([]any)) != len(exported["comments"].([]any)) {
		t.Fatalf("comment counts differ: %d vs %d", len(gotComments), len(wantComments))
	}
	for k, v := range wantThreads {
		if gotThreads[k] != v {
			t.Errorf("thread %s:\n got %s\nwant %s", k, gotThreads[k], v)
		}
	}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range wantComments {
			if !yield(k) {
				return
			}
		}
	}) {
		if gotComments[k] != wantComments[k] {
			t.Errorf("comment %q:\n got %s\nwant %s", k, gotComments[k], wantComments[k])
		}
	}
	// The imported guest comments can be claimed by address.
	n, err := dst.st.ClaimCounts(t.Context(), dst.keys.EmailHMAC("gail@example.com"))
	if err != nil || n.Count != 6 {
		t.Errorf("claimable = %+v, %v", n, err)
	}
}

// upload sends an export file to the import endpoint.
func (in *instance) upload(raw []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("format", "json")
	fw, _ := mw.CreateFormFile("file", "export.json")
	_, _ = fw.Write(raw)
	_ = mw.Close()
	return in.do("POST", "/api/v1/sites/"+in.site.PublicID+"/imports", mw.FormDataContentType(), &buf)
}
