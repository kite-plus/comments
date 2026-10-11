// Package spam holds the content rules of docs/design/moderation.md section
// 3 that need no database: name cleaning, link hosts, blocked words and the
// platform's spam domains. The counting rules run in the API against the
// store.
package spam

import (
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/idna"

	"github.com/kite-plus/comments/internal/policy"
)

// invisible are zero-width and bidirectional control characters, which can
// make one name look like another.
func invisible(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u2060', '\u2061', '\u2062', '\u2063', '\u2064',
		'\ufeff', '\u061c', '\u180e', '\u00ad', '\u034f', '\u115f', '\u1160', '\u3164', '\uffa0':
		return true
	}
	return (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') || unicode.Is(unicode.Cc, r)
}

// CleanName strips invisible characters and collapses whitespace in a
// display name or guest nickname.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

var hrefRE = regexp.MustCompile(`href="([^"]+)"`)

// Hosts lists the hosts a rendered comment links to, lower case.
func Hosts(renderedHTML string) []string {
	var out []string
	for _, m := range hrefRE.FindAllStringSubmatch(renderedHTML, -1) {
		raw := strings.ReplaceAll(m[1], "&amp;", "&")
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			out = append(out, strings.ToLower(u.Hostname()))
		}
	}
	return out
}

// NormalizeDomain is the form domains are listed and matched in: lower
// case, punycode, no trailing dot, so an internationalized name cannot slip
// past its listed punycode form or the other way round.
func NormalizeDomain(d string) string {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	if ascii, err := idna.Lookup.ToASCII(d); err == nil {
		return ascii
	}
	return d
}

// DomainListed reports whether host is a listed domain or below one; the
// list holds normalized domains.
func DomainListed(host string, listed map[string]bool) bool {
	host = NormalizeDomain(host)
	for host != "" {
		if listed[host] {
			return true
		}
		_, rest, ok := strings.Cut(host, ".")
		if !ok {
			break
		}
		host = rest
	}
	return false
}

// BlockedWord returns the first of words found in text, ignoring case.
func BlockedWord(text string, words []string) (string, bool) {
	lower := strings.ToLower(text)
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w != "" && strings.Contains(lower, strings.ToLower(w)) {
			return w, true
		}
	}
	return "", false
}

// NewAccount reports whether an account created at created is still new
// (moderation.md section 3.3).
func NewAccount(created, now time.Time) bool { return now.Sub(created) < policy.NewAccountAge }

// NormalizeWords cleans a site's blocked word list: trimmed, no empties, no
// duplicates ignoring case.
func NormalizeWords(words []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, w := range words {
		w = strings.TrimSpace(w)
		k := strings.ToLower(w)
		if w == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, w)
	}
	return out
}
