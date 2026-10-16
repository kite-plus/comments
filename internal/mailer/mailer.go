// Package mailer builds and sends the service's mail (docs/design/
// moderation.md section 6): reply and owner notifications, confirmation and
// claim mail, in Chinese and English, plain text with an HTML part whose
// images travel inline, with RFC 8058 one-click unsubscribe headers and no
// tracking.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"path"
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

// The images mail.html shows as cid:<name>@kite.plus travel inside the
// message, so opening it fetches nothing and no inbox blocks them.
//
//go:embed img/*.png
var images embed.FS

func (s *Sender) compose(from *mail.Address, m Message) ([]byte, error) {
	if strings.ContainsAny(m.To, "\r\n") {
		return nil, errors.New("bad recipient")
	}
	var b bytes.Buffer
	alt := multipart.NewWriter(&b)
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
	h("Content-Type", `multipart/alternative; boundary="`+alt.Boundary()+`"`)
	b.WriteString("\r\n")
	if err := textPart(alt, "text/plain", m.Text); err != nil {
		return nil, err
	}
	if m.HTML != "" {
		relBoundary := randomHex(15)
		pw, err := alt.CreatePart(textproto.MIMEHeader{"Content-Type": {`multipart/related; type="text/html"; boundary="` + relBoundary + `"`}})
		if err != nil {
			return nil, err
		}
		rel := multipart.NewWriter(pw)
		if err := rel.SetBoundary(relBoundary); err != nil {
			return nil, err
		}
		if err := textPart(rel, "text/html", m.HTML); err != nil {
			return nil, err
		}
		if err := imageParts(rel); err != nil {
			return nil, err
		}
		if err := rel.Close(); err != nil {
			return nil, err
		}
	}
	if err := alt.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func textPart(w *multipart.Writer, ct, body string) error {
	pw, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {ct + "; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(pw)
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return err
	}
	return qp.Close()
}

func imageParts(w *multipart.Writer) error {
	names, err := images.ReadDir("img")
	if err != nil {
		return err
	}
	for _, n := range names {
		data, err := images.ReadFile("img/" + n.Name())
		if err != nil {
			return err
		}
		id := strings.TrimSuffix(n.Name(), path.Ext(n.Name()))
		pw, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {`image/png; name="` + n.Name() + `"`},
			"Content-Transfer-Encoding": {"base64"},
			"Content-ID":                {"<" + id + "@kite.plus>"},
			"Content-Disposition":       {`inline; filename="` + n.Name() + `"`},
		})
		if err != nil {
			return err
		}
		if err := writeBase64(pw, data); err != nil {
			return err
		}
	}
	return nil
}

// writeBase64 wraps at 76 columns as RFC 2045 requires.
func writeBase64(w io.Writer, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		if _, err := io.WriteString(w, enc[:76]+"\r\n"); err != nil {
			return err
		}
		enc = enc[76:]
	}
	_, err := io.WriteString(w, enc+"\r\n")
	return err
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
