package importer

import (
	"strings"
	"testing"
)

func TestWaline(t *testing.T) {
	r, err := Parse(FormatWaline, []byte(`{"type":"waline","version":1,"data":{
		"Comment":[
			{"objectId":"1","comment":"hi ![x](data:image/png;base64,AAA)","insertedAt":"2023-05-01T10:00:00.000Z","mail":"A@B.example","nick":"Ann","url":"/posts/a/","status":"approved","ip":"1.2.3.4","ua":"UA","sticky":1},
			{"objectId":"2","comment":"re","insertedAt":"2023-05-02T10:00:00.000Z","mail":"","nick":"Bob","url":"/posts/a/","status":"waiting","pid":"1","rid":"1"}
		],
		"Users":[{"objectId":"u","password":"$hash","2fa":"secret"}]}}`))
	if err != nil || len(r.Comments) != 2 {
		t.Fatalf("parse = %+v, %v", r, err)
	}
	a, b := r.Comments[0], r.Comments[1]
	if a.Email != "a@b.example" || !a.Pinned || a.BodyMD != "hi [图片]" || a.Status != "published" {
		t.Errorf("first = %+v", a)
	}
	if b.ParentSourceID != "1" || b.Status != "pending" || b.Email != "" {
		t.Errorf("second = %+v", b)
	}
}

func TestTwikooLines(t *testing.T) {
	data := `{"_id":{"$oid":"a1"},"nick":"N","mail":"n@x.example","url":"/p","comment":"<p>Hello <strong>you</strong></p>","created":{"$numberLong":"1680000000000"},"uid":"md5-of-admin-password","top":true}
{"_id":{"$oid":"a2"},"nick":"M","mail":"","href":"https://x.example/p","comment":"<p>re</p>","pid":"a1","created":1680000100000,"isSpam":true}`
	r, err := Parse(FormatTwikoo, []byte(data))
	if err != nil || len(r.Comments) != 2 {
		t.Fatalf("parse = %+v, %v", r, err)
	}
	if r.Comments[0].BodyMD != "Hello **you**" || !r.Comments[0].Pinned || r.Comments[1].Status != "spam" {
		t.Errorf("comments = %+v", r.Comments)
	}
	for _, c := range r.Comments {
		if strings.Contains(c.BodyMD+c.AuthorRef+c.AuthorURL, "md5-of-admin") {
			t.Error("Twikoo uid imported")
		}
	}
}

func TestArtrans(t *testing.T) {
	r, err := Parse(FormatArtrans, []byte(`[{"id":"5","rid":"0","content":"x","date":"2022-01-02 03:04:05","nick":"A","email":"anonymous@example.org","page_key":"https://blog.example/p/","page_title":"P","is_pending":"true","is_pinned":"false","ip":"1.1.1.1","password":""}]`))
	if err != nil || len(r.Comments) != 1 {
		t.Fatalf("parse = %+v, %v", r, err)
	}
	if c := r.Comments[0]; c.Email != "" || c.Status != "pending" || c.ParentSourceID != "" {
		t.Errorf("comment = %+v", c)
	}
}

func TestWXR(t *testing.T) {
	data := `<?xml version="1.0"?><rss xmlns:wp="http://wordpress.org/export/1.2/"><channel><item>
<title>Hello</title><link>https://wp.example/?p=12</link><wp:post_id>12</wp:post_id>
<wp:comment><wp:comment_id>3</wp:comment_id><wp:comment_author>Ann</wp:comment_author><wp:comment_author_email>ann@x.example</wp:comment_author_email>
<wp:comment_author_IP>1.2.3.4</wp:comment_author_IP><wp:comment_date_gmt>2020-01-01 00:00:00</wp:comment_date_gmt>
<wp:comment_content><![CDATA[<a href="https://x.example">link</a> and <em>this</em>]]></wp:comment_content><wp:comment_approved>1</wp:comment_approved><wp:comment_type>comment</wp:comment_type><wp:comment_parent>0</wp:comment_parent></wp:comment>
<wp:comment><wp:comment_id>4</wp:comment_id><wp:comment_author>Pinger</wp:comment_author><wp:comment_date_gmt>2020-01-02 00:00:00</wp:comment_date_gmt><wp:comment_content>ping</wp:comment_content><wp:comment_approved>1</wp:comment_approved><wp:comment_type>pingback</wp:comment_type></wp:comment>
<wp:comment><wp:comment_id>5</wp:comment_id><wp:comment_author>Bob</wp:comment_author><wp:comment_date_gmt>2020-01-03 00:00:00</wp:comment_date_gmt><wp:comment_content>gone</wp:comment_content><wp:comment_approved>trash</wp:comment_approved><wp:comment_type></wp:comment_type><wp:comment_parent>3</wp:comment_parent></wp:comment>
</item></channel></rss>`
	r, err := Parse(FormatWXR, []byte(data))
	if err != nil || len(r.Comments) != 2 {
		t.Fatalf("parse = %+v, %v", r, err)
	}
	if c := r.Comments[0]; c.BodyMD != "[link](https://x.example) and *this*" || c.PostID != "12" || c.SourceID != "12:3" {
		t.Errorf("first = %+v", c)
	}
	if c := r.Comments[1]; c.Status != "deleted" || c.ParentSourceID != "12:3" {
		t.Errorf("second = %+v", c)
	}
}

func TestFlatten(t *testing.T) {
	list := []Comment{{SourceID: "c", ParentSourceID: "b"}, {SourceID: "a"}, {SourceID: "b", ParentSourceID: "a"}}
	ordered, root := Flatten(list)
	if ordered[0].SourceID != "a" || ordered[1].SourceID != "b" || ordered[2].SourceID != "c" {
		t.Errorf("order = %v", ordered)
	}
	if root["b"] != "a" || root["c"] != "a" || root["a"] != "" {
		t.Errorf("roots = %v", root)
	}
}

func TestMapping(t *testing.T) {
	m := Mapping{Rewrites: []Rewrite{{From: "https://old.example", To: "https://new.example"}}}
	if got := m.Apply("/a", "https://old.example"); got != "https://new.example/a" {
		t.Errorf("Apply = %q", got)
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	cases := map[string]string{
		"<p>a</p><p>b</p>":                        "a\n\nb",
		"x<br>y":                                  "x\ny",
		"<blockquote>q</blockquote>after":         "> q\n\nafter",
		"<ul><li>one</li><li>two</li></ul>":       "- one\n- two",
		"<img src=x> <script>alert(1)</script>t":  "[图片] t",
		"<a href=\"javascript:alert(1)\">bad</a>": "bad",
		"5 * 3 _x_":                               `5 \* 3 \_x\_`,
		"<pre><code>a &lt; b</code></pre>":        "```\na < b\n```",
	}
	for in, want := range cases {
		if got := HTMLToMarkdown(in); got != want {
			t.Errorf("HTMLToMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}
