// Package pagekey turns a page into its discussion identifier, by the rules
// of docs/design/widget.md section 5, and normalizes site origins as
// docs/design/sites.md section 2 describes. Those sections are the only
// statement of the rules; this package is their only implementation.
package pagekey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

const (
	maxExplicitRunes = 200
	maxPathKeyRunes  = 512
)

var (
	ErrBadKey    = errors.New("page key must be 1 to 200 characters without control characters")
	ErrBadURL    = errors.New("page address is not an absolute http or https URL")
	ErrBadOrigin = errors.New("origin is not scheme, host and optional port")
)

// Explicit returns the identifier for a page-key attribute.
func Explicit(raw string) (string, error) {
	k := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(k)
	if n < 1 || n > maxExplicitRunes || !utf8.ValidString(k) || strings.ContainsFunc(k, unicode.IsControl) {
		return "", ErrBadKey
	}
	return "k:" + k, nil
}

// FromURL returns the identifier for a page address and that address's
// origin. The caller checks the origin against the site's origins.
func FromURL(raw string, queryKeys []string) (key, origin string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", "", ErrBadURL
	}
	origin, err = originOf(u)
	if err != nil {
		return "", "", ErrBadURL
	}
	p := "p:" + normalizePath(u.EscapedPath())
	if q := keptQuery(u, queryKeys); q != "" {
		p += "?" + q
	}
	if utf8.RuneCountInString(p) > maxPathKeyRunes {
		sum := sha256.Sum256([]byte(p))
		return "h:" + hex.EncodeToString(sum[:]), origin, nil
	}
	return p, origin, nil
}

// Valid reports whether key has the shape of an identifier this package
// produces.
func Valid(key string) bool {
	switch {
	case strings.HasPrefix(key, "k:"):
		_, err := Explicit(key[2:])
		return err == nil && strings.TrimSpace(key[2:]) == key[2:]
	case strings.HasPrefix(key, "p:/"):
		return utf8.RuneCountInString(key) <= maxPathKeyRunes
	case strings.HasPrefix(key, "h:"):
		_, err := hex.DecodeString(key[2:])
		return err == nil && len(key) == 2+64
	}
	return false
}

// NormalizeOrigin returns raw as "scheme://host[:port]": lower case,
// punycode, no default port, nothing else.
func NormalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrBadOrigin
	}
	return originOf(u)
}

// OriginOf returns the normalized origin of an absolute URL.
func OriginOf(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ErrBadURL
	}
	return originOf(u)
}

func originOf(u *url.URL) (string, error) {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if host == "" {
		return "", ErrBadOrigin
	}
	if strings.Contains(host, ":") {
		if net.ParseIP(host) == nil {
			return "", ErrBadOrigin
		}
		host = "[" + host + "]"
	} else {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return "", ErrBadOrigin
		}
		host = ascii
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		for _, r := range port {
			if r < '0' || r > '9' {
				return "", ErrBadOrigin
			}
		}
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// normalizePath applies widget.md section 5 rule 4 to an escaped path.
func normalizePath(escaped string) string {
	p := decodePath(escaped)
	p = norm.NFC.String(p)
	p = removeDots(p)
	for _, suffix := range []string{"/index.html", "/index.htm"} {
		if s, ok := strings.CutSuffix(p, suffix); ok {
			p = s + "/"
			break
		}
	}
	for _, suffix := range []string{".html", ".htm"} {
		if s, ok := strings.CutSuffix(p, suffix); ok && !strings.HasSuffix(s, "/") && s != "" {
			p = s
			break
		}
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		p = "/"
	}
	return p
}

// decodePath decodes percent escapes into UTF-8, except for the characters
// that would change the structure ("/", "?", "#", "%"), control characters,
// and byte runs that are not valid UTF-8. Kept escapes are upper-cased.
func decodePath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Collect a run of escapes and decode the longest valid prefix.
		var raw []byte
		j := i
		for j+2 < len(s) && s[j] == '%' && isHex(s[j+1]) && isHex(s[j+2]) {
			raw = append(raw, unhex(s[j+1])<<4|unhex(s[j+2]))
			j += 3
		}
		if len(raw) == 0 {
			b.WriteByte('%')
			i++
			continue
		}
		for k := 0; k < len(raw); {
			r, size := utf8.DecodeRune(raw[k:])
			if r == utf8.RuneError || r == '/' || r == '?' || r == '#' || r == '%' || unicode.IsControl(r) {
				for _, c := range raw[k : k+max(size, 1)] {
					b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
				}
				k += max(size, 1)
				continue
			}
			b.WriteRune(r)
			k += size
		}
		i = j
	}
	return b.String()
}

func removeDots(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	trailing := strings.HasSuffix(p, "/")
	var out []string
	for seg := range strings.SplitSeq(p[1:], "/") {
		switch seg {
		case ".":
			trailing = true
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			trailing = true
		default:
			out = append(out, seg)
			trailing = false
		}
	}
	res := "/" + strings.Join(out, "/")
	if trailing && !strings.HasSuffix(res, "/") {
		res += "/"
	}
	return res
}

func keptQuery(u *url.URL, keys []string) string {
	if len(keys) == 0 || u.RawQuery == "" {
		return ""
	}
	q := u.Query()
	kept := url.Values{}
	for _, k := range slices.Sorted(slices.Values(keys)) {
		if v, ok := q[k]; ok {
			kept[k] = v
		}
	}
	return kept.Encode()
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}
