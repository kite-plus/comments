// Package mailer builds and sends the service's mail (docs/design/
// moderation.md section 6): reply and owner notifications, confirmation and
// claim mail, in Chinese and English, plain text with a simple HTML part,
// with RFC 8058 one-click unsubscribe headers and no tracking.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/kite-plus/comments/internal/crypt"
)

// Message is one mail.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	// Unsubscribe is the one-click unsubscribe URL, when the mail is a
	// notification.
	Unsubscribe string
}

// Sender delivers mail over SMTP.
type Sender struct {
	URL  *url.URL // smtp://[user:pass@]host:port or smtps://...
	From string
	// Hostname names this side in HELO and Message-ID.
	Hostname string
}

// NewSender parses COMMENTS_SMTP_URL.
func NewSender(rawURL, from, hostname string) (*Sender, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Host == "" {
		return nil, errors.New("not an smtp:// or smtps:// URL")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, err
	}
	return &Sender{URL: u, From: from, Hostname: hostname}, nil
}

// Send delivers one message.
func (s *Sender) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return err
	}
	raw, err := s.compose(from, m)
	if err != nil {
		return err
	}
	host := s.URL.Hostname()
	port := s.URL.Port()
	if port == "" {
		port = "25"
		if s.URL.Scheme == "smtps" {
			port = "465"
		}
	}
	addr := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if s.URL.Scheme == "smtps" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello(s.helloName()); err != nil {
		return err
	}
	if s.URL.Scheme == "smtp" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	if user := s.URL.User; user != nil {
		pass, _ := user.Password()
		if err := c.Auth(smtp.PlainAuth("", user.Username(), pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *Sender) helloName() string {
	if s.Hostname != "" {
		return s.Hostname
	}
	return "localhost"
}

func (s *Sender) compose(from *mail.Address, m Message) ([]byte, error) {
	var b bytes.Buffer
	boundary := randomHex(12)
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", (&mail.Address{Name: from.Name, Address: from.Address}).String())
	h("To", m.To)
	h("Subject", mime.BEncoding.Encode("UTF-8", m.Subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+randomHex(16)+"@"+s.helloName()+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	if m.Unsubscribe != "" {
		h("List-Unsubscribe", "<"+m.Unsubscribe+">")
		h("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ ct, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		if part.body == "" {
			continue
		}
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.ct)
		qp := quotedprintable.NewWriter(&b)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n"))); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Token purposes.
const (
	PurposeUnsubscribe = "unsubscribe"
	PurposeMute        = "mute"
)

// Unsubscribe payloads: what one click stops.
const (
	UnsubReplies = "u" // a reader's reply mail: u:<user id>
	UnsubGuest   = "g" // all mail to a guest address: g:<hex email hmac>
	UnsubOwner   = "o" // a site's owner mail: o:<site id>:<user id>
)

// UnsubscribeURL is the link for one-click unsubscribe; it works without
// state, so old mail keeps working.
func UnsubscribeURL(base string, keys *crypt.Keys, payload string) string {
	return strings.TrimRight(base, "/") + "/unsubscribe?t=" + url.QueryEscape(keys.Sign(PurposeUnsubscribe, payload))
}

// MuteURL is the link that stops a reader's reply mail from one thread.
func MuteURL(base string, keys *crypt.Keys, userID, threadID int64) string {
	return strings.TrimRight(base, "/") + "/mute?t=" + url.QueryEscape(keys.Sign(PurposeMute, fmt.Sprintf("%d:%d", userID, threadID)))
}

// ParseMute reads a mute token.
func ParseMute(keys *crypt.Keys, token string) (userID, threadID int64, err error) {
	p, err := keys.Verify(PurposeMute, token)
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscanf(p, "%d:%d", &userID, &threadID); err != nil {
		return 0, 0, crypt.ErrBadToken
	}
	return userID, threadID, nil
}

// decodeHeader is for tests that look at a subject.
func decodeHeader(v string) string {
	d, err := new(mime.WordDecoder).DecodeHeader(v)
	if err != nil {
		return v
	}
	return d
}
