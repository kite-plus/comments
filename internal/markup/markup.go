// Package markup is the only package that produces HTML from what readers
// write: goldmark renders the Markdown subset of
// docs/design/moderation.md section 5 and bluemonday sanitizes the result
// against an allowlist.
package markup

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Version is comments.render_version. Raise it whenever the rendered output
// for the same Markdown changes; the worker then renders old comments again.
const Version = 1

const linkRel = "ugc nofollow noopener noreferrer"

var (
	md = goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
		goldmark.WithRendererOptions(html.WithHardWraps()),
		goldmark.WithRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(
			util.Prioritized(html.NewRenderer(html.WithHardWraps()), 1000),
			util.Prioritized(extension.NewStrikethroughHTMLRenderer(), 500),
			util.Prioritized(&restricted{}, 100),
		))),
	)
	sanitizer = newSanitizer()
	strict    = bluemonday.StrictPolicy()
	spaces    = regexp.MustCompile(`\s+`)
)

func newSanitizer() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "em", "del", "code", "pre", "blockquote", "ul", "ol", "li", "hr")
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^` + linkRel + `$`)).OnElements("a")
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[A-Za-z0-9_+-]{1,30}$`)).OnElements("code")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(false)
	return p
}

// Result is a rendered comment.
type Result struct {
	HTML  string
	Links int
}

// Render turns a comment's Markdown into safe HTML and counts its links.
func Render(source string) Result {
	src := []byte(source)
	doc := md.Parser().Parse(text.NewReader(src))
	links := 0
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n.Kind() {
			case ast.KindLink, ast.KindAutoLink, ast.KindImage:
				links++
			}
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		// Rendering into a buffer does not fail; keep the text visible anyway.
		return Result{HTML: "<p>" + stdhtml.EscapeString(source) + "</p>", Links: links}
	}
	return Result{HTML: sanitizer.Sanitize(buf.String()), Links: links}
}

// Excerpt returns the first n characters of a comment as plain text, for
// mail.
func Excerpt(source string, n int) string {
	plain := stdhtml.UnescapeString(strict.Sanitize(Render(source).HTML))
	plain = strings.TrimSpace(spaces.ReplaceAllString(plain, " "))
	if utf8.RuneCountInString(plain) <= n {
		return plain
	}
	r := []rune(plain)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// PlainText strips sanitized HTML to text, for imported comments that have
// only HTML.
func PlainText(htmlText string) string {
	return strings.TrimSpace(stdhtml.UnescapeString(strict.Sanitize(htmlText)))
}

// restricted renders what moderation.md section 5 leaves out: headings as
// bold paragraphs, images as links, raw HTML as text, and every link with
// the fixed rel and target.
type restricted struct{}

func (r *restricted) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, r.heading)
	reg.Register(ast.KindImage, r.image)
	reg.Register(ast.KindLink, r.link)
	reg.Register(ast.KindAutoLink, r.autoLink)
	reg.Register(ast.KindRawHTML, r.rawHTML)
	reg.Register(ast.KindHTMLBlock, r.htmlBlock)
	reg.Register(extast.KindStrikethrough, r.strike)
}

func (r *restricted) heading(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<p><strong>")
	} else {
		_, _ = w.WriteString("</strong></p>\n")
	}
	return ast.WalkContinue, nil
}

func (r *restricted) strike(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<del>")
	} else {
		_, _ = w.WriteString("</del>")
	}
	return ast.WalkContinue, nil
}

func openLink(w util.BufWriter, dest []byte) bool {
	if !allowedURL(string(dest)) {
		return false
	}
	_, _ = w.WriteString(`<a href="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape(dest, true)))
	_, _ = w.WriteString(`" rel="` + linkRel + `" target="_blank">`)
	return true
}

func (r *restricted) link(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	ok := allowedURL(string(n.Destination))
	if entering {
		if ok {
			openLink(w, n.Destination)
		}
	} else if ok {
		_, _ = w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

func (r *restricted) autoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	dest := n.URL(source)
	label := n.Label(source)
	if n.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(dest), []byte("mailto:")) {
		dest = append([]byte("mailto:"), dest...)
	} else if n.AutoLinkType == ast.AutoLinkURL && bytes.HasPrefix(bytes.ToLower(dest), []byte("www.")) {
		dest = append([]byte("http://"), dest...)
	}
	if openLink(w, dest) {
		_, _ = w.Write(util.EscapeHTML(label))
		_, _ = w.WriteString("</a>")
	} else {
		_, _ = w.Write(util.EscapeHTML(label))
	}
	return ast.WalkSkipChildren, nil
}

func (r *restricted) image(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	alt := nodeText(n, source)
	if len(alt) == 0 {
		alt = n.Destination
	}
	if openLink(w, n.Destination) {
		_, _ = w.Write(util.EscapeHTML(alt))
		_, _ = w.WriteString("</a>")
	} else {
		_, _ = w.Write(util.EscapeHTML(alt))
	}
	return ast.WalkSkipChildren, nil
}

func (r *restricted) rawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.RawHTML)
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			_, _ = w.Write(util.EscapeHTML(seg.Value(source)))
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r *restricted) htmlBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.HTMLBlock)
	var b bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		b.Write(line.Value(source))
	}
	if n.HasClosure() {
		b.Write(n.ClosureLine.Value(source))
	}
	_, _ = w.WriteString("<p>")
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i, l := range lines {
		if i > 0 {
			_, _ = w.WriteString("<br>\n")
		}
		_, _ = w.Write(util.EscapeHTML([]byte(l)))
	}
	_, _ = w.WriteString("</p>\n")
	return ast.WalkSkipChildren, nil
}

func nodeText(n ast.Node, source []byte) []byte {
	var b bytes.Buffer
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	return b.Bytes()
}

func allowedURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}
