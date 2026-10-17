// Package devblogs serves the two local test blogs that embed the widget,
// on different sites: blog A (light) and blog B (dark). They are for
// development and the end-to-end tests only.
package devblogs

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// Fixed site IDs, so the seed and the pages agree.
const (
	SiteA = "0a000000-0000-4000-8000-00000000000a"
	SiteB = "0b000000-0000-4000-8000-00000000000b"
)

// Blog is one test blog.
type Blog struct {
	Name   string
	SiteID string
	Dark   bool
	Posts  []Post
	// Embed is the widget's base URL, the service's public URL or a
	// self-hosted copy.
	Embed string
}

// Post is one article.
type Post struct {
	Path    string
	Title   string
	PageKey string
	Body    string
}

// A returns blog A's content.
func A(embed string) Blog {
	return Blog{Name: "Blog A", SiteID: SiteA, Embed: embed, Posts: []Post{
		{Path: "/posts/hello/", Title: "Hello from blog A", Body: "The first post on the light test blog."},
		{Path: "/posts/second/", Title: "A post with a page key", PageKey: "post-second", Body: "This page names its thread with page-key."},
	}}
}

// B returns blog B's content.
func B(embed string) Blog {
	return Blog{Name: "Blog B", SiteID: SiteB, Dark: true, Embed: embed, Posts: []Post{
		{Path: "/posts/welcome/", Title: "Welcome to blog B", Body: "The dark test blog."},
		{Path: "/coop/", Title: "A page with COOP same-origin", Body: "Login falls back to the full-page redirect here (roadmap T10)."},
	}}
}

// Handler serves the blog.
func (b Blog) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var list strings.Builder
		for _, p := range b.Posts {
			fmt.Fprintf(&list, `<li><a href="%s">%s</a></li>`, p.Path, html.EscapeString(p.Title))
		}
		b.write(w, b.Name, "<h1>"+html.EscapeString(b.Name)+"</h1><ul>"+list.String()+"</ul>", "")
	})
	for _, p := range b.Posts {
		mux.HandleFunc("GET "+p.Path, func(w http.ResponseWriter, r *http.Request) {
			if p.Path == "/coop/" {
				w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			}
			attrs := ` site="` + b.SiteID + `"`
			if p.PageKey != "" {
				attrs += ` page-key="` + p.PageKey + `"`
			}
			if r.URL.Query().Get("lazy") == "false" {
				attrs += ` lazy="false"`
			}
			body := `<article><h1>` + html.EscapeString(p.Title) + `</h1><p>` + html.EscapeString(p.Body) + `</p>` +
				strings.Repeat(`<p class="filler">Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p>`, 3) + `</article>` +
				`<section id="comments"><kite-comments` + attrs + `></kite-comments></section>` +
				`<script src="` + strings.TrimRight(b.Embed, "/") + `/embed.js" crossorigin="anonymous" async></script>`
			b.write(w, p.Title, body, `<link rel="canonical" href="`+canonical(r, p.Path)+`">`)
		})
	}
	return mux
}

func canonical(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func (b Blog) write(w http.ResponseWriter, title, body, head string) {
	bg, fg := "#ffffff", "#1f2328"
	if b.Dark {
		bg, fg = "#0d1117", "#e6edf3"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>%s<style>body{margin:0;background:%s;color:%s;font:16px/1.7 system-ui,sans-serif}main{max-width:44rem;margin:2rem auto;padding:0 1rem}a{color:inherit}</style></head>
<body><main><p><a href="/">%s</a></p>%s</main></body></html>`, lang(b), html.EscapeString(title), head, bg, fg, html.EscapeString(b.Name), body)
}

func lang(b Blog) string {
	if b.Dark {
		return "en-US"
	}
	return "zh-Hans"
}
