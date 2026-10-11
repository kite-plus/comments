package api

import (
	"crypto/sha256"
	"encoding/base64"
	"html"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// pageCSS styles the service's own small pages: the login popup, the
// confirmation, and the pages mail links open.
const pageCSS = `:root{color-scheme:light dark;--fg:#1f2328;--muted:#59636e;--bg:#fff;--line:#d1d9e0;--accent:#3d65bd}` +
	`@media (prefers-color-scheme:dark){:root{--fg:#e6edf3;--muted:#9198a1;--bg:#0d1117;--line:#3d444d;--accent:#7c9fe8}}` +
	`body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif}` +
	`main{max-width:28rem;margin:12vh auto;padding:0 1.25rem}h1{font-size:1.25rem;margin:0 0 .75rem}p{margin:.5rem 0;color:var(--muted)}` +
	`strong{color:var(--fg)}form{margin-top:1.5rem;display:flex;gap:.75rem;flex-wrap:wrap}` +
	`button{font:inherit;padding:.5rem 1.1rem;border-radius:.4rem;border:1px solid var(--line);background:transparent;color:var(--fg);cursor:pointer}` +
	`button.primary{background:var(--accent);border-color:var(--accent);color:#fff}[hidden]{display:none!important}` +
	`.foot{margin-top:2rem;font-size:.85rem}`

var pageCSSHash = func() string {
	sum := sha256.Sum256([]byte(pageCSS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// pageOpts describes one of the service's small pages.
type pageOpts struct {
	title  string
	body   string // trusted HTML built with esc
	script string // path under /static/widget/, or ""
	// formTargets are extra origins the page's forms may lead to.
	formTargets []string
	status      int
	// data attributes on <body>, escaped here.
	data map[string]string
}

// writePage renders a small page. None of these may be framed, and none
// sets COOP: the login popup must keep its opener (login.md section 4.2).
func (s *Server) writePage(c *gin.Context, o pageOpts) {
	lang := "zh-CN"
	if !zh(c) {
		lang = "en"
	}
	form := "'self'"
	for _, t := range o.formTargets {
		form += " " + t
	}
	csp := "default-src 'none'; script-src 'self'; style-src " + pageCSSHash + "; img-src 'self' data:; connect-src 'self'; " +
		"form-action " + form + "; base-uri 'none'; frame-ancestors 'none'"
	c.Header("Content-Security-Policy", csp)
	// Forms here post to the service itself, which needs a real Origin
	// header; see threadDocument.
	c.Header("Referrer-Policy", "strict-origin")
	c.Header("Cache-Control", "no-store")
	c.Header("Vary", "Accept-Language")
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + lang + `"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="strict-origin">`)
	b.WriteString(`<title>` + esc(o.title) + `</title><style>` + pageCSS + `</style>`)
	if o.script != "" {
		b.WriteString(`<script type="module" src="/static/widget/` + esc(o.script) + `"></script>`)
	}
	b.WriteString(`</head><body`)
	for k, v := range o.data {
		b.WriteString(` data-` + esc(k) + `="` + esc(v) + `"`)
	}
	b.WriteString(`><main>` + o.body + `<p class="foot">` + esc(t(c, "Kite Plus 评论", "Kite Plus Comments")) + `</p></main></body></html>`)
	status := o.status
	if status == 0 {
		status = http.StatusOK
	}
	c.Data(status, "text/html; charset=utf-8", []byte(b.String()))
}

// messagePage is a page with a heading and a paragraph.
func (s *Server) messagePage(c *gin.Context, status int, title, text string) {
	s.writePage(c, pageOpts{title: title, status: status, body: `<h1>` + esc(title) + `</h1><p>` + esc(text) + `</p>`})
}

func esc(s string) string { return html.EscapeString(s) }

// t picks the Chinese or English text by the request's language.
func t(c *gin.Context, zhText, enText string) string {
	if zh(c) {
		return zhText
	}
	return enText
}
