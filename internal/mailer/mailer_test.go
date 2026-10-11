package mailer

import (
	"bytes"
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
	m := Replies("zh-CN", "reader@example.com", []Item{{SiteName: "某博客", ThreadTitle: "你好", Permalink: "https://b.example/p#kc-1",
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
	if strings.Contains(string(raw), "<img") {
		t.Error("tracking pixel")
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
