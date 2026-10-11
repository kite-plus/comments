// Package verify proves control of a site origin by DNS TXT record, HTML
// meta tag or file (docs/design/sites.md section 2), and fetches from other
// people's servers only at public addresses.
package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"

	"github.com/kite-plus/comments/internal/policy"
)

// Record names (sites.md section 2).
const (
	TXTPrefix = "_kite-comments."
	TXTValue  = "kite-comments-site="
	MetaName  = "kite-comments-site"
	FilePath  = "/.well-known/kite-comments.txt"
)

var (
	ErrBlocked  = errors.New("address is not public")
	ErrTooLarge = errors.New("response exceeds the size limit")
)

// Fetcher gets URLs with the protections of sites.md section 2: public
// addresses on ports 80 and 443 only, bounded time and size, and redirects
// only within the same host.
type Fetcher struct {
	// AllowPrivate lifts the address and port checks, for development.
	AllowPrivate bool
	// LookupTXT replaces DNS lookups in tests.
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	client    *http.Client
}

// NewFetcher returns a Fetcher.
func NewFetcher(allowPrivate bool) *Fetcher {
	f := &Fetcher{AllowPrivate: allowPrivate}
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: f.control}
	f.client = &http.Client{
		Timeout: policy.VerifyTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err == nil && allowPrivate && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
					// Browsers send *.localhost to loopback; the resolver may not.
					addr = net.JoinHostPort("127.0.0.1", port)
				}
				return dialer.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: policy.VerifyTimeout,
			MaxIdleConnsPerHost:   1,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > policy.VerifyMaxRedirects {
				return errors.New("too many redirects")
			}
			if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
				return errors.New("redirect leaves the host")
			}
			return nil
		},
	}
	return f
}

func (f *Fetcher) control(_, address string, _ syscall.RawConn) error {
	if f.AllowPrivate {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if port != "80" && port != "443" {
		return fmt.Errorf("%w: port %s", ErrBlocked, port)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !Public(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, ip)
	}
	return nil
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

// Public reports whether ip is a public unicast address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Get fetches rawURL and returns at most maxBytes of its body, failing on
// a larger one, along with the content type.
func (f *Fetcher) Get(ctx context.Context, rawURL string, maxBytes int64) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("not an http or https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "KitePlusComments/1 (+https://comments.kite.plus)")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", ErrTooLarge
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// Result is the outcome of checking one origin.
type Result struct {
	Origin string `json:"origin"`
	OK     bool   `json:"ok"`
	Method string `json:"method,omitempty"` // dns, meta or file
	// Detail says what was found, for the owner (api.md verification_failed).
	Detail string `json:"detail,omitempty"`
}

// Check looks for a token accepted by matches at origin, by DNS, then the
// home page's meta tag, then the file.
func (f *Fetcher) Check(ctx context.Context, origin string, matches func(token string) bool) Result {
	res := Result{Origin: origin}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		res.Detail = "not an origin"
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 3*policy.VerifyTimeout)
	defer cancel()
	var notes []string

	lookup := f.LookupTXT
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	if records, err := lookup(ctx, TXTPrefix+u.Hostname()); err == nil {
		for _, r := range records {
			if v, ok := strings.CutPrefix(strings.TrimSpace(r), TXTValue); ok && matches(strings.TrimSpace(v)) {
				res.OK, res.Method = true, "dns"
				return res
			}
		}
		notes = append(notes, fmt.Sprintf("DNS: %d TXT record(s) at %s%s, none matching", len(records), TXTPrefix, u.Hostname()))
	} else {
		notes = append(notes, "DNS: no TXT record at "+TXTPrefix+u.Hostname())
	}

	if body, _, err := f.Get(ctx, origin+"/", policy.VerifyMaxBytes); err == nil {
		tokens := MetaTokens(body)
		for _, tok := range tokens {
			if matches(tok) {
				res.OK, res.Method = true, "meta"
				return res
			}
		}
		if len(tokens) > 0 {
			notes = append(notes, "meta: the home page's kite-comments-site tag does not match")
		} else {
			notes = append(notes, "meta: the home page has no kite-comments-site tag")
		}
	} else {
		notes = append(notes, "meta: home page unreachable: "+err.Error())
	}

	if body, _, err := f.Get(ctx, origin+FilePath, 4096); err == nil {
		if matches(strings.TrimSpace(string(body))) {
			res.OK, res.Method = true, "file"
			return res
		}
		notes = append(notes, "file: "+FilePath+" does not match")
	} else {
		notes = append(notes, "file: "+FilePath+" unreachable: "+err.Error())
	}
	res.Detail = strings.Join(notes, "; ")
	return res
}

// MetaTokens returns the content of every kite-comments-site meta tag in a
// page's head.
func MetaTokens(page []byte) []string {
	var out []string
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "body" {
				return out
			}
			if t.Data != "meta" {
				continue
			}
			var name, content string
			for _, a := range t.Attr {
				switch strings.ToLower(a.Key) {
				case "name":
					name = strings.ToLower(strings.TrimSpace(a.Val))
				case "content":
					content = strings.TrimSpace(a.Val)
				}
			}
			if name == MetaName && content != "" {
				out = append(out, content)
			}
		}
	}
}
