package mailer

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/kite-plus/comments/internal/crypt"
)

func TestCompose(t *testing.T) {
	s, err := NewSender("smtp://localhost:1025", "Kite Plus 评论 <notify@kite.plus>", "comments.kite.plus")
	if err != nil {
		t.Fatal(err)
	}
	m := Replies("https://comments.example", "zh-CN", "reader@example.com", []Item{{SiteName: "某博客", ThreadTitle: "你好", Permalink: "https://b.example/p#kc-1",
		Author: "Ann", Excerpt: "回复内容", MuteURL: "https://c.example/mute?t=x"}}, "https://c.example/unsubscribe?t=y")
	from, _ := mail.ParseAddress(s.From)
	raw, err := s.compose(from, m)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeHeader(msg.Header.Get("Subject")); got != "Ann 回复了你在「某博客」的评论" {
		t.Errorf("subject = %q", got)
	}
	if msg.Header.Get("List-Unsubscribe") != "<https://c.example/unsubscribe?t=y>" || msg.Header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Errorf("unsubscribe headers = %v", msg.Header)
	}
	if strings.Contains(m.HTML, `src="http`) || strings.Contains(m.HTML, `src="//`) {
		t.Error("remote image in the HTML part")
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	alt := multipart.NewReader(msg.Body, params["boundary"])
	var cids []string
	for {
		p, err := alt.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		ct, ps, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if ct != "multipart/related" {
			continue
		}
		rel := multipart.NewReader(p, ps["boundary"])
		for {
			q, err := rel.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			if id := q.Header.Get("Content-Id"); id != "" {
				cids = append(cids, id)
			}
		}
	}
	for _, id := range cids {
		if !strings.Contains(m.HTML, `src="cid:`+strings.Trim(id, "<>")+`"`) {
			t.Errorf("inline part %s is not used", id)
		}
	}
	if len(cids) != 2 {
		t.Errorf("inline images = %v", cids)
	}
	if !strings.Contains(m.Text, "回复内容") || !strings.Contains(m.Text, "https://c.example/mute?t=x") || !strings.Contains(m.HTML, "《你好》") {
		t.Errorf("body:\n%s", m.Text)
	}
}

func TestTemplatesEscape(t *testing.T) {
	m := Owner("https://comments.example", "en", "o@example.com", "<Blog>", []Item{{ThreadTitle: "<b>t</b>", Permalink: "javascript:alert(1)",
		Author: "<i>x</i>", Excerpt: "<script>", Pending: false}}, 1, "https://comments.example/console", "", false)
	for _, bad := range []string{"<b>t", "<i>x", "<script>", "javascript:"} {
		if strings.Contains(m.HTML, bad) {
			t.Errorf("HTML carries %q", bad)
		}
	}
	if m.Subject != "1 new comment on <Blog>" {
		t.Errorf("subject = %q", m.Subject)
	}
}

func TestMuteToken(t *testing.T) {
	d, _ := crypt.ParseDataKeys("1:" + strings.Repeat("A", 43) + "=")
	k, err := crypt.New(d, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	u := MuteURL("https://c.example", k, 7, 9)
	tok := u[strings.Index(u, "t=")+2:]
	tok = strings.ReplaceAll(tok, "%3D", "=")
	if uid, tid, err := ParseMute(k, tok); err != nil || uid != 7 || tid != 9 {
		t.Errorf("ParseMute = %d, %d, %v", uid, tid, err)
	}
}
