package importer

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kite-plus/comments/internal/pagekey"
)

// Thread is a thread an import writes to.
type Thread struct {
	Key         string
	URL         string
	Title       string
	PublishedAt *time.Time
	Locked      bool
}

// Planned is a comment with its thread worked out.
type Planned struct {
	Comment
	Key          string
	RootSourceID string
}

// Plan is what an import will write, parents before replies.
type Plan struct {
	Threads  []Thread
	Comments []Planned
}

// Site is what planning needs to know about the importing site.
type Site struct {
	Origins   []string
	Primary   string
	QueryKeys []string
}

// MakePlan maps every comment's address, computes its thread identifier
// by docs/design/widget.md section 5, and leaves out comments whose
// address is not on the site. The summary is the preview of sites.md
// section 7, step 2.
func MakePlan(r Result, m Mapping, site Site) (Plan, Summary) {
	sum := Summary{Total: len(r.Comments), ByStatus: map[string]int{}, Failures: r.Failures, Unmatched: []string{}, Merged: []Merge{}}
	if sum.Failures == nil {
		sum.Failures = []Failure{}
	}
	pages := map[string]map[string]bool{}
	unmatched := map[string]bool{}
	threads := map[string]Thread{}
	kept := map[string]Planned{}
	var order []Comment
	for _, c := range r.Comments {
		sum.ByStatus[c.Status]++
		if c.HasEmail() {
			sum.WithEmail++
		}
		if !c.CreatedAt.IsZero() {
			if sum.First == nil || c.CreatedAt.Before(*sum.First) {
				t := c.CreatedAt
				sum.First = &t
			}
			if sum.Last == nil || c.CreatedAt.After(*sum.Last) {
				t := c.CreatedAt
				sum.Last = &t
			}
		}
		url := m.Apply(c.Page, site.Primary)
		key, origin, err := pagekey.FromURL(url, site.QueryKeys)
		switch {
		case c.ThreadKey != "" && pagekey.Valid(c.ThreadKey):
			key = c.ThreadKey
		case c.PostID != "" && m.WordPressKeys == "post_id":
			key = "k:wp:" + c.PostID
		}
		if err != nil || !slices.Contains(site.Origins, origin) {
			sum.UnmatchedNum++
			label := origin
			if label == "" {
				label = c.Page
			}
			unmatched[label] = true
			continue
		}
		if pages[key] == nil {
			pages[key] = map[string]bool{}
		}
		pages[key][c.Page] = true
		if _, ok := threads[key]; !ok {
			title := c.PageTitle
			threads[key] = Thread{Key: key, URL: url, Title: title, PublishedAt: c.PublishedAt, Locked: c.ThreadLock}
		}
		kept[c.SourceID] = Planned{Comment: c, Key: key}
		order = append(order, c)
	}
	// Replies whose parent did not make it, or sits in another thread,
	// become top-level comments.
	for i, c := range order {
		if p, ok := kept[c.ParentSourceID]; c.ParentSourceID != "" && (!ok || p.Key != kept[c.SourceID].Key) {
			order[i].ParentSourceID = ""
			x := kept[c.SourceID]
			x.ParentSourceID = ""
			kept[c.SourceID] = x
		}
	}
	ordered, roots := Flatten(order)
	var plan Plan
	for _, c := range ordered {
		x := kept[c.SourceID]
		if x.ParentSourceID != "" {
			x.RootSourceID = roots[c.SourceID]
		}
		plan.Comments = append(plan.Comments, x)
	}
	for _, k := range slices.Sorted(maps.Keys(threads)) {
		plan.Threads = append(plan.Threads, threads[k])
	}
	sum.Threads = len(threads)
	for _, k := range slices.Sorted(maps.Keys(pages)) {
		if len(pages[k]) > 1 {
			sum.Merged = append(sum.Merged, Merge{Key: k, Pages: slices.Sorted(maps.Keys(pages[k]))})
		}
	}
	sum.Unmatched = slices.Sorted(maps.Keys(unmatched))
	if len(sum.Unmatched) > 50 {
		sum.Unmatched = sum.Unmatched[:50]
	}
	return plan, sum
}

// AuthorKind is "guest" for a comment that can be claimed by its address,
// else "external" (data-model.md section 3.4).
func AuthorKind(c Comment) string {
	if c.HasEmail() {
		return "guest"
	}
	return "external"
}

// AuthorName fits a name into the column's limits.
func AuthorName(c Comment) string {
	n := strings.TrimSpace(c.AuthorName)
	if n == "" {
		n = "匿名"
	}
	if r := []rune(n); len(r) > 40 {
		n = string(r[:40])
	}
	return n
}
