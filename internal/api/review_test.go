package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/store"
)

func TestReturnPathsStayLocal(t *testing.T) {
	for _, p := range []string{"/me", "/sites/1?tab=threads", "/me#comments"} {
		if !localPath(p) {
			t.Errorf("%q refused", p)
		}
	}
	for _, p := range []string{"", "//evil.example", "/\\evil.example", "\\\\evil.example", "/\t/evil.example", "\t//evil.example",
		"/\n/evil.example", "/\r\n/evil.example", "/x\x7f", "/\x00", "https://evil.example/", "javascript:alert(1)", "evil.example",
		"/x\\..\\evil", "///evil.example"} {
		if localPath(p) {
			t.Errorf("%q accepted", p)
		}
	}
	e := newEnv(t)
	for _, raw := range []string{blogPage + "\t", "https://blog.example/\n/x", "https://blog.example/\x7f"} {
		if _, ok := e.srv.returnURL(e.site, raw); ok {
			t.Errorf("site return %q accepted", raw)
		}
	}
	// A TAB in the console's return falls back to /me.
	e.idp.SignIn("ann")
	w := e.do(req{method: "GET", path: "/auth/login?return=" + url.QueryEscape("/\t/evil.example")})
	binder := cookie(w, "comments_binder")
	w = e.do(req{method: "GET", path: e.authorize(w.Header().Get("Location")), cookies: []*http.Cookie{binder}})
	if w.Header().Get("Location") != "/me" {
		t.Errorf("console login returned to %q", w.Header().Get("Location"))
	}
}

func TestSiteCreationLimits(t *testing.T) {
	e := newEnv(t)
	session, token := e.consoleLogin("owner")
	h, c := csrf(token, session)
	create := func(i int) int {
		return e.do(req{method: "POST", path: "/api/v1/sites", body: map[string]any{"name": "S", "origin": fmt.Sprintf("https://s%d.example", i),
			"guest_policy": "off"}, headers: h, cookies: c}).Code
	}
	for i := range policy.SiteCreatesPerUser.Max {
		if code := create(i); code != http.StatusCreated {
			t.Fatalf("site %d = %d", i, code)
		}
	}
	if code := create(99); code != http.StatusTooManyRequests {
		t.Errorf("site past the daily limit = %d", code)
	}
	// The cap counts every site the account holds.
	session, token = e.consoleLogin("bob")
	h, c = csrf(token, session)
	bob, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "bob")
	for i := range policy.SitesPerAccount {
		if _, err := e.st.CreateSite(t.Context(), store.NewSite{Name: "B", Origins: []string{fmt.Sprintf("https://b%d.example", i)}, CreatedBy: &bob.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if code := create(100); code != http.StatusForbidden {
		t.Errorf("site past the cap = %d", code)
	}
}

func multipartImport(t *testing.T, format, data string) (string, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("format", format)
	fw, err := mw.CreateFormFile("file", "export.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(data))
	_ = mw.Close()
	return b.String(), mw.FormDataContentType()
}

const walineExport = `{"type":"waline","version":1,"data":{"Comment":[
	{"objectId":"1","comment":"old","insertedAt":"2020-05-01T10:00:00.000Z","mail":"guest@example.com","nick":"G","url":"/posts/a/","status":"approved"}]}}`

func TestImportGatesAndLimits(t *testing.T) {
	e := newEnv(t)
	session, token := e.consoleLogin("owner")
	h, c := csrf(token, session)
	owner, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	unvetted, err := e.st.CreateSite(t.Context(), store.NewSite{Name: "New", Origins: []string{"https://new.example"}, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddMember(t.Context(), e.site.ID, owner.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	upload := func(site model.Site, data string) *http.Response {
		body, ct := multipartImport(t, "waline", data)
		w := e.do(req{method: "POST", path: "/api/v1/sites/" + site.PublicID + "/imports", body: body,
			headers: with(h, "Content-Type", ct), cookies: c})
		return w.Result()
	}
	if r := upload(unvetted, walineExport); r.StatusCode != http.StatusForbidden {
		t.Errorf("import on an unapproved site = %d", r.StatusCode)
	}
	if r := upload(e.site, walineExport); r.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d", r.StatusCode)
	}
	// What is kept is the parsed comments, the address encrypted.
	var stored []byte
	if err := e.db.QueryRow(context.Background(), `SELECT file FROM imports ORDER BY id DESC LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("guest@example.com")) || bytes.Contains(stored, []byte(`"objectId"`)) || !bytes.Contains(stored, []byte(`"EmailHMAC"`)) {
		t.Errorf("stored import = %s", stored)
	}
	for i := 1; i < policy.ImportUploadsPerSite.Max; i++ {
		if r := upload(e.site, walineExport); r.StatusCode != http.StatusCreated {
			t.Fatalf("upload %d = %d", i, r.StatusCode)
		}
	}
	if r := upload(e.site, walineExport); r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("upload past the daily limit = %d", r.StatusCode)
	}
	// A site at its quota of imported comments takes no more.
	if _, err := e.db.Exec(context.Background(), `INSERT INTO threads (site_id, page_key, url) VALUES ($1, 'p:/q', 'https://other.example/q')`, e.other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(context.Background(), `INSERT INTO comments (site_id, thread_id, author_kind, author_name, body_md, body_html,
		render_version, status, source, source_id)
		SELECT $1, t.id, 'guest', 'G', 'x', 'x', 1, 'published', 'waline', g::text
		FROM threads t, generate_series(1, $2::int) g WHERE t.site_id = $1`, e.other.ID, policy.ImportedPerSite); err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddMember(t.Context(), e.other.ID, owner.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if r := upload(e.other, walineExport); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("upload past the quota = %d", r.StatusCode)
	}
}

func TestBannedReaderCannotEdit(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	w := e.post(token, e.site, "first", nil)
	id := decode(t, w)["id"].(string)
	ann, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann")
	if _, err := e.st.CreateBan(t.Context(), store.Ban{SiteID: &e.site.ID, Issuer: e.idp.Issuer, Subject: "ann"}, ann.ID); err != nil {
		t.Fatal(err)
	}
	w = e.do(req{method: "PATCH", path: "/api/v1/comments/" + id, body: map[string]any{"site": e.site.PublicID, "body": "changed"}, headers: bearer(token)})
	if w.Code != http.StatusForbidden {
		t.Errorf("edit while banned = %d %s", w.Code, w.Body)
	}
}

func TestAuthorDeletesOnAnySite(t *testing.T) {
	e := newEnv(t)
	token, ck := e.popupLogin("ann", e.site)
	one := decode(t, e.post(token, e.site, "one", nil))["id"].(string)
	two := decode(t, e.post(token, e.site, "two", nil))["id"].(string)
	bobToken, _ := e.popupLogin("bob", e.site)
	bobs := decode(t, e.post(bobToken, e.site, "bob's", nil))["id"].(string)
	if err := e.st.SuspendSite(t.Context(), e.site.ID, "abuse"); err != nil {
		t.Fatal(err)
	}
	// From the widget, on a suspended site.
	if w := e.do(req{method: "DELETE", path: "/api/v1/comments/" + one + "?site=" + e.site.PublicID, headers: own(), cookies: []*http.Cookie{ck}}); w.Code != http.StatusNoContent {
		t.Errorf("widget delete on a suspended site = %d %s", w.Code, w.Body)
	}
	// From the reader center.
	session, csrfToken := e.consoleLogin("ann")
	h, c := csrf(csrfToken, session)
	if w := e.do(req{method: "DELETE", path: "/api/v1/me/comments/" + two, headers: h, cookies: c}); w.Code != http.StatusNoContent {
		t.Errorf("reader center delete = %d %s", w.Code, w.Body)
	}
	if w := e.do(req{method: "DELETE", path: "/api/v1/me/comments/" + two, headers: h, cookies: c}); w.Code != http.StatusNotFound {
		t.Errorf("second delete = %d", w.Code)
	}
	if w := e.do(req{method: "DELETE", path: "/api/v1/me/comments/" + bobs, headers: h, cookies: c}); w.Code != http.StatusNotFound {
		t.Errorf("deleting another reader's comment = %d", w.Code)
	}
	if w := e.do(req{method: "DELETE", path: "/api/v1/me/comments/" + bobs, cookies: c}); w.Code != http.StatusForbidden {
		t.Errorf("delete without CSRF token = %d", w.Code)
	}
	for _, id := range []string{one, two} {
		n, _ := strconv.ParseInt(id, 10, 64)
		if cm, _ := e.st.Comment(t.Context(), n); cm.Status != model.StatusDeleted {
			t.Errorf("comment %s = %s", id, cm.Status)
		}
	}
}

func TestSiteCookieSlidesWithTheSession(t *testing.T) {
	e := newEnv(t)
	_, ck := e.popupLogin("ann", e.site)
	if _, err := e.db.Exec(context.Background(), `UPDATE site_sessions SET last_used_at = now() - interval '2 days', expires_at = now() + interval '1 day'`); err != nil {
		t.Fatal(err)
	}
	w := e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: own(), cookies: []*http.Cookie{ck}})
	again := cookie(w, ck.Name)
	if again == nil || again.Value != ck.Value || !again.Partitioned || again.MaxAge < 2*24*3600 {
		t.Fatalf("slid cookie = %+v", again)
	}
	// The bearer carrier has no cookie to refresh.
	if _, err := e.db.Exec(context.Background(), `UPDATE site_sessions SET last_used_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	w = e.do(req{method: "GET", path: "/api/v1/widget/viewer?site=" + e.site.PublicID, headers: bearer(ck.Value)})
	if cookie(w, ck.Name) != nil {
		t.Error("cookie set for a bearer request")
	}
}

func TestConsolePagesOnlyWithTheirPolicy(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/static/console/index.html", "/static/console/", "/static/console/sub/page.html"} {
		if w := e.do(req{method: "GET", path: p}); w.Code != http.StatusNotFound {
			t.Errorf("%s = %d", p, w.Code)
		}
	}
	if w := e.do(req{method: "GET", path: "/me"}); !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors") {
		t.Errorf("console page CSP = %q", w.Header().Get("Content-Security-Policy"))
	}
}

func TestPublishedDatesInTheFuture(t *testing.T) {
	e := newEnv(t)
	token, _ := e.popupLogin("ann", e.site)
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	if w := e.post(token, e.site, "early", map[string]any{"published": future}); w.Code != http.StatusCreated {
		t.Fatalf("post = %d %s", w.Code, w.Body)
	}
	thread, err := e.st.ThreadByKey(t.Context(), e.site.ID, blogKey)
	if err != nil || thread.PagePublishedAt != nil {
		t.Errorf("future published date kept: %v %v", thread.PagePublishedAt, err)
	}
	session, csrfToken := e.consoleLogin("owner")
	h, c := csrf(csrfToken, session)
	owner, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	if err := e.st.AddMember(t.Context(), e.site.ID, owner.ID, model.RoleOwner); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/sites/" + e.site.PublicID + "/threads/" + strconv.FormatInt(thread.ID, 10)
	if w := e.do(req{method: "PATCH", path: path, body: map[string]any{"published_at": future}, headers: h, cookies: c}); w.Code != http.StatusBadRequest {
		t.Errorf("owner sets a future date = %d", w.Code)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if w := e.do(req{method: "PATCH", path: path, body: map[string]any{"published_at": past, "title": "Fixed"}, headers: h, cookies: c}); w.Code != http.StatusOK {
		t.Errorf("owner corrects the thread = %d %s", w.Code, w.Body)
	}
	if x, _ := e.st.ThreadByKey(t.Context(), e.site.ID, blogKey); x.Title != "Fixed" || x.PagePublishedAt == nil {
		t.Errorf("thread = %+v", x)
	}
}

func TestSpamDomainsCoverHomepages(t *testing.T) {
	e := newEnv(t)
	e.consoleLogin("owner")
	admin, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "owner")
	for _, d := range []string{"spam.example", "xn--bcher-kva.example"} {
		if err := e.st.AddSpamDomain(t.Context(), d, admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	token, _ := e.popupLogin("ann", e.site)
	ann, _ := e.st.UserBySubject(t.Context(), e.idp.Issuer, "ann")
	status := func(w string) string {
		var s string
		if err := e.db.QueryRow(context.Background(), `SELECT status FROM comments WHERE id = $1`, w).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := e.st.UpdateProfile(t.Context(), ann.ID, store.ProfileUpdate{HomepageURL: new("https://www.bücher.example/")}); err != nil {
		t.Fatal(err)
	}
	id := decode(t, e.post(token, e.site, "plain words", nil))["id"].(string)
	if s := status(id); s != model.StatusSpam {
		t.Errorf("comment from a listed IDN homepage = %s", s)
	}
	if _, err := e.st.UpdateProfile(t.Context(), ann.ID, store.ProfileUpdate{HomepageURL: new("https://ann.example/")}); err != nil {
		t.Fatal(err)
	}
	id = decode(t, e.post(token, e.site, "more plain words", nil))["id"].(string)
	if s := status(id); s != model.StatusPublished {
		t.Errorf("comment from a fine homepage = %s", s)
	}
}
