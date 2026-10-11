// Package importer reads other comment systems' export files into one
// shape (docs/design/sites.md section 7): this service's own JSON, Waline,
// Twikoo, Artrans and WordPress WXR. It never keeps the source systems'
// secrets: Waline's Users table, Twikoo's uid, IP addresses and user agents
// are not read.
package importer

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Formats.
const (
	FormatJSON    = "json"
	FormatWaline  = "waline"
	FormatTwikoo  = "twikoo"
	FormatArtrans = "artrans"
	FormatWXR     = "wxr"
)

// Formats lists what Parse reads.
var Formats = []string{FormatJSON, FormatWaline, FormatTwikoo, FormatArtrans, FormatWXR}

var ErrFormat = errors.New("not a file of this format")

// Comment is one imported comment.
type Comment struct {
	SourceID       string
	ParentSourceID string
	// Page is the page address as the source has it: absolute, or a path
	// for formats that store only the path.
	Page      string
	PageTitle string
	// ThreadKey is set when the source names the discussion identifier
	// itself (this service's JSON).
	ThreadKey   string
	AuthorName  string
	Email       string // "" when there is none to claim with
	AuthorURL   string
	AuthorRef   string
	BodyMD      string
	Status      string // published, pending, spam, hidden or deleted
	Pinned      bool
	CreatedAt   time.Time
	EditedAt    *time.Time
	ThreadLock  bool
	PublishedAt *time.Time // the page's, for this service's JSON
	PostID      string     // WordPress's post ID, for wp:<id> keys
}

// Failure is an entry that could not be read.
type Failure struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// Result is a parsed file.
type Result struct {
	Comments []Comment
	Failures []Failure
}

// Parse reads a file of the given format.
func Parse(format string, data []byte) (Result, error) {
	switch format {
	case FormatJSON:
		return parseOwn(data)
	case FormatWaline:
		return parseWaline(data)
	case FormatTwikoo:
		return parseTwikoo(data)
	case FormatArtrans:
		return parseArtrans(data)
	case FormatWXR:
		return parseWXR(data)
	}
	return Result{}, fmt.Errorf("unknown format %q", format)
}

var dataImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*data:[^)]*\)`)

// cleanMarkdown replaces images embedded as base64, which Waline does by
// default, with a marker (sites.md section 7, step 4).
func cleanMarkdown(s string) string {
	return strings.TrimSpace(dataImage.ReplaceAllString(s, "[图片]"))
}

// placeholderEmail reports addresses that stand for "none".
func placeholderEmail(e string) bool {
	e = strings.ToLower(strings.TrimSpace(e))
	return e == "" || e == "anonymous@example.org" || !strings.Contains(e, "@")
}

func cleanEmail(e string) string {
	if placeholderEmail(e) {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(e))
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02T15:04:05.000Z", "2006-01-02"}

func parseTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		for _, l := range timeLayouts {
			if t, err := time.Parse(l, x); err == nil {
				return t.UTC(), true
			}
		}
	case float64:
		if x > 1e12 {
			return time.UnixMilli(int64(x)).UTC(), true
		}
		if x > 0 {
			return time.Unix(int64(x), 0).UTC(), true
		}
	case map[string]any:
		// MongoDB extended JSON: {"$date": ...} or {"$numberLong": "..."}.
		for _, k := range []string{"$date", "$numberLong"} {
			if inner, ok := x[k]; ok {
				if s, ok := inner.(string); ok && k == "$numberLong" {
					var n float64
					if _, err := fmt.Sscan(s, &n); err == nil {
						return parseTime(n)
					}
				}
				return parseTime(inner)
			}
		}
	}
	return time.Time{}, false
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case map[string]any:
		if s, ok := x["$oid"].(string); ok {
			return s
		}
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func boolean(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	}
	return false
}

// Summary is the preview of an import (sites.md section 7, step 2).
type Summary struct {
	Total        int            `json:"total"`
	ByStatus     map[string]int `json:"by_status"`
	First        *time.Time     `json:"first,omitempty"`
	Last         *time.Time     `json:"last,omitempty"`
	WithEmail    int            `json:"with_email"`
	Threads      int            `json:"threads"`
	Failures     []Failure      `json:"failures"`
	Unmatched    []string       `json:"unmatched_origins"`
	Merged       []Merge        `json:"merged"`
	UnmatchedNum int            `json:"unmatched_comments"`
}

// Merge lists source pages that become one thread.
type Merge struct {
	Key   string   `json:"key"`
	Pages []string `json:"pages"`
}

// Mapping is the owner's address rules (sites.md section 7, step 3).
type Mapping struct {
	// Origin completes addresses that are only a path.
	Origin   string    `json:"origin,omitempty"`
	Rewrites []Rewrite `json:"rewrites,omitempty"`
	// WordPressKeys is "url" (default) or "post_id", which keys WXR
	// threads as wp:<post id> the way the WordPress plugin will.
	WordPressKeys string `json:"wordpress_keys,omitempty"`
}

// Rewrite replaces a prefix of an address.
type Rewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Apply returns the address a comment's page maps to.
func (m Mapping) Apply(page, fallbackOrigin string) string {
	u := strings.TrimSpace(page)
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		origin := m.Origin
		if origin == "" {
			origin = fallbackOrigin
		}
		u = strings.TrimRight(origin, "/") + u
	}
	for _, r := range m.Rewrites {
		if r.From != "" && strings.HasPrefix(u, r.From) {
			u = r.To + strings.TrimPrefix(u, r.From)
		}
	}
	return u
}

// Flatten orders comments so parents come before replies and works out
// each one's top-level ancestor: the source's deep nesting becomes two
// levels, keeping who replied to whom (data-model.md section 3.4).
func Flatten(list []Comment) (ordered []Comment, root map[string]string) {
	byID := map[string]Comment{}
	for _, c := range list {
		byID[c.SourceID] = c
	}
	root = map[string]string{}
	var find func(id string, depth int) string
	find = func(id string, depth int) string {
		c, ok := byID[id]
		if !ok || c.ParentSourceID == "" || depth > 1000 {
			return id
		}
		if _, ok := byID[c.ParentSourceID]; !ok {
			return id
		}
		return find(c.ParentSourceID, depth+1)
	}
	depth := map[string]int{}
	for _, c := range list {
		r := find(c.SourceID, 0)
		if r != c.SourceID {
			root[c.SourceID] = r
		}
		d := 0
		for p := c.ParentSourceID; p != "" && d < 1000; d++ {
			pc, ok := byID[p]
			if !ok {
				break
			}
			p = pc.ParentSourceID
		}
		depth[c.SourceID] = d
	}
	ordered = slices.Clone(list)
	slices.SortStableFunc(ordered, func(a, b Comment) int {
		if c := cmp.Compare(depth[a.SourceID], depth[b.SourceID]); c != 0 {
			return c
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return ordered, root
}
