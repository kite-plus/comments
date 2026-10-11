package importer

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var blankLines = regexp.MustCompile(`\n{3,}`)

// HTMLToMarkdown turns the HTML of Twikoo and WordPress comments into the
// Markdown subset comments use. Tags it does not know keep only their
// text; images become a marker (sites.md section 7, step 4).
func HTMLToMarkdown(src string) string {
	doc, err := html.Parse(strings.NewReader("<body>" + src + "</body>"))
	if err != nil {
		return strings.TrimSpace(src)
	}
	b := &strings.Builder{}
	var walk func(n *html.Node, list string, depth int)
	children := func(n *html.Node, list string, depth int) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, list, depth)
		}
	}
	walk = func(n *html.Node, list string, depth int) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(escapeText(n.Data))
			return
		case html.ElementNode:
		default:
			children(n, list, depth)
			return
		}
		switch n.Data {
		case "script", "style", "iframe", "object", "template":
		case "br":
			b.WriteString("\n")
		case "p", "div":
			b.WriteString("\n\n")
			children(n, list, depth)
			b.WriteString("\n\n")
		case "strong", "b":
			b.WriteString("**")
			children(n, list, depth)
			b.WriteString("**")
		case "em", "i":
			b.WriteString("*")
			children(n, list, depth)
			b.WriteString("*")
		case "del", "s", "strike":
			b.WriteString("~~")
			children(n, list, depth)
			b.WriteString("~~")
		case "code":
			b.WriteString("`" + textOf(n) + "`")
		case "pre":
			b.WriteString("\n\n```\n" + strings.TrimRight(textOf(n), "\n") + "\n```\n\n")
		case "blockquote":
			saved := b
			b = &strings.Builder{}
			children(n, list, depth)
			text := strings.TrimSpace(b.String())
			b = saved
			b.WriteString("\n\n")
			for line := range strings.SplitSeq(text, "\n") {
				b.WriteString("> " + line + "\n")
			}
			b.WriteString("\n")
		case "ul", "ol":
			b.WriteString("\n\n")
			children(n, n.Data, depth+1)
			b.WriteString("\n")
		case "li":
			b.WriteString(strings.Repeat("  ", max(depth-1, 0)))
			if list == "ol" {
				b.WriteString("1. ")
			} else {
				b.WriteString("- ")
			}
			children(n, list, depth)
			b.WriteString("\n")
		case "a":
			href := attr(n, "href")
			label := strings.TrimSpace(textOf(n))
			if href == "" || (!strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") && !strings.HasPrefix(href, "mailto:")) {
				b.WriteString(escapeText(label))
			} else if label == "" || label == href {
				b.WriteString("<" + href + ">")
			} else {
				b.WriteString("[" + escapeText(label) + "](" + strings.ReplaceAll(href, ")", "%29") + ")")
			}
		case "img":
			b.WriteString("[图片]")
		default:
			children(n, list, depth)
		}
	}
	walk(doc, "", 0)
	out := blankLines.ReplaceAllString(b.String(), "\n\n")
	return strings.TrimSpace(out)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.Data == "br" {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// escapeText keeps text from turning into Markdown or HTML by accident.
func escapeText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "<", "&lt;", "[", `\[`, "]", `\]`)
	return r.Replace(s)
}
