package markup

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestSupported(t *testing.T) {
	cases := map[string]string{
		"hello\nworld":                    "<p>hello<br>\nworld</p>",
		"**b** *i* ~~s~~ `c`":             "<p><strong>b</strong> <em>i</em> <del>s</del> <code>c</code></p>",
		"> quote":                         "<blockquote>\n<p>quote</p>\n</blockquote>",
		"- a\n- b":                        "<ul>\n<li>a</li>\n<li>b</li>\n</ul>",
		"```go\nx := 1\n```":              "<pre><code class=\"language-go\">x := 1\n</code></pre>",
		"# Title":                         "<p><strong>Title</strong></p>",
		"[x](https://a.example/?q=1&r=2)": `<p><a href="https://a.example/?q=1&amp;r=2" rel="ugc nofollow noopener noreferrer" target="_blank">x</a></p>`,
		"see https://a.example/x":         `<p>see <a href="https://a.example/x" rel="ugc nofollow noopener noreferrer" target="_blank">https://a.example/x</a></p>`,
		"![cat](https://a.example/c.png)": `<p><a href="https://a.example/c.png" rel="ugc nofollow noopener noreferrer" target="_blank">cat</a></p>`,
		"a <b>raw</b> tag":                "<p>a &lt;b&gt;raw&lt;/b&gt; tag</p>",
		"<div>block</div>":                "<p>&lt;div&gt;block&lt;/div&gt;</p>",
		"| a | b |\n|---|---|\n| 1 | 2 |": "<p>| a | b |<br>\n|---|---|<br>\n| 1 | 2 |</p>",
	}
	for in, want := range cases {
		if got := strings.TrimSpace(Render(in).HTML); got != want {
			t.Errorf("Render(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// Known XSS vectors; none may produce markup that runs script or loads
// anything.
func TestXSS(t *testing.T) {
	vectors := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`[x](javascript:alert(1))`,
		`[x](JaVaScRiPt:alert(1))`,
		`[x](  javascript:alert(1))`,
		`[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)`,
		`[x](vbscript:msgbox(1))`,
		`![x](javascript:alert(1))`,
		`<a href="javascript:alert(1)">x</a>`,
		`<svg onload=alert(1)>`,
		`<iframe src="https://evil.example"></iframe>`,
		`[x](https://a.example/" onmouseover="alert(1))`,
		"`<script>`",
		`<style>body{}</style>`,
		`<!-- <script>alert(1)</script> -->`,
		`[x](//evil.example/path)`,
		`[x](/relative)`,
		`<math><mi xlink:href="javascript:alert(1)">x</mi></math>`,
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		`<a href="&#106;avascript:alert(1)">x</a>`,
		`[x](&#106;avascript:alert(1))`,
		`<details open ontoggle=alert(1)>`,
	}
	for _, v := range vectors {
		checkSafe(t, v, Render(v).HTML)
	}
}

// checkSafe parses out and fails on any element, attribute or URL outside
// the allowlist; escaped text is harmless.
func checkSafe(t *testing.T, in, out string) {
	t.Helper()
	allowed := map[string]bool{"p": true, "br": true, "strong": true, "em": true, "del": true, "code": true,
		"pre": true, "blockquote": true, "ul": true, "ol": true, "li": true, "hr": true, "a": true}
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if !allowed[tok.Data] {
			t.Errorf("Render(%q) has element %q: %s", in, tok.Data, out)
		}
		for _, a := range tok.Attr {
			switch {
			case tok.Data == "a" && a.Key == "href":
				if !strings.HasPrefix(a.Val, "https://") && !strings.HasPrefix(a.Val, "http://") && !strings.HasPrefix(a.Val, "mailto:") {
					t.Errorf("Render(%q) links to %q", in, a.Val)
				}
			case tok.Data == "a" && (a.Key == "rel" || a.Key == "target"):
			case tok.Data == "code" && a.Key == "class":
			case tok.Data == "ol" && a.Key == "start":
			default:
				t.Errorf("Render(%q) has attribute %s=%q on %s", in, a.Key, a.Val, tok.Data)
			}
		}
	}
}

func TestLinksCounted(t *testing.T) {
	r := Render("[a](https://a.example) https://b.example ![c](https://c.example/x.png) <d@e.example>")
	if r.Links != 4 {
		t.Errorf("links = %d, want 4", r.Links)
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt("**Hi** <b>there</b>\n\nsecond", 200); got != "Hi <b>there</b> second" {
		t.Errorf("Excerpt = %q", got)
	}
	if got := Excerpt(strings.Repeat("字", 210), 200); len([]rune(got)) != 201 {
		t.Errorf("long excerpt has %d runes", len([]rune(got)))
	}
}
