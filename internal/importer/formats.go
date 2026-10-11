package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// parseOwn reads this service's export (docs/export-format.md). Readers'
// comments come in as external authors: their accounts stay with the
// instance they were written on (sites.md section 7).
func parseOwn(data []byte) (Result, error) {
	var f struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Threads []struct {
			ID          string     `json:"id"`
			Key         string     `json:"key"`
			URL         string     `json:"url"`
			Title       string     `json:"title"`
			PublishedAt *time.Time `json:"published_at"`
			Locked      bool       `json:"locked"`
		} `json:"threads"`
		Comments []struct {
			ID       string `json:"id"`
			ThreadID string `json:"thread_id"`
			ParentID string `json:"parent_id"`
			Author   struct {
				Kind  string `json:"kind"`
				Name  string `json:"name"`
				URL   string `json:"url"`
				Email string `json:"email"`
				Ref   string `json:"ref"`
			} `json:"author"`
			Body struct {
				Format string `json:"format"`
				Source string `json:"source"`
			} `json:"body"`
			Status    string     `json:"status"`
			Pinned    bool       `json:"pinned"`
			CreatedAt time.Time  `json:"created_at"`
			EditedAt  *time.Time `json:"edited_at"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Format != "kite-comments" {
		return Result{}, ErrFormat
	}
	if f.Version != 1 {
		return Result{}, fmt.Errorf("export version %d is not supported", f.Version)
	}
	type thread struct {
		key, url, title string
		published       *time.Time
		locked          bool
	}
	threads := map[string]thread{}
	for _, t := range f.Threads {
		threads[t.ID] = thread{t.Key, t.URL, t.Title, t.PublishedAt, t.Locked}
	}
	var r Result
	for i, c := range f.Comments {
		t, ok := threads[c.ThreadID]
		if !ok || c.ID == "" {
			r.Failures = append(r.Failures, Failure{i, "unknown thread or missing id"})
			continue
		}
		name := c.Author.Name
		email := ""
		if c.Author.Kind == "guest" {
			email = cleanEmail(c.Author.Email)
		}
		if name == "" && c.Author.Kind == "former_user" {
			name = "已注销用户"
		}
		r.Comments = append(r.Comments, Comment{
			SourceID: c.ID, ParentSourceID: c.ParentID, Page: t.url, PageTitle: t.title, ThreadKey: t.key,
			AuthorName: name, Email: email, AuthorURL: c.Author.URL, AuthorRef: c.Author.Ref, BodyMD: c.Body.Source,
			Status: normalStatus(c.Status), Pinned: c.Pinned, CreatedAt: c.CreatedAt, EditedAt: c.EditedAt,
			ThreadLock: t.locked, PublishedAt: t.published,
		})
	}
	return r, nil
}

func normalStatus(s string) string {
	switch s {
	case "published", "pending", "spam", "hidden", "deleted":
		return s
	}
	return "published"
}

// parseWaline reads Waline's admin export: {type: "waline", version: 1,
// data: {Comment: [...]}}. Only the Comment table is read.
func parseWaline(data []byte) (Result, error) {
	var f struct {
		Type string `json:"type"`
		Data struct {
			Comment []map[string]any `json:"Comment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Type != "waline" {
		return Result{}, ErrFormat
	}
	var r Result
	for i, m := range f.Data.Comment {
		id := str(m["objectId"])
		if id == "" {
			id = str(m["id"])
		}
		created, ok := parseTime(m["insertedAt"])
		if !ok {
			created, ok = parseTime(m["createdAt"])
		}
		if id == "" || str(m["url"]) == "" || !ok {
			r.Failures = append(r.Failures, Failure{i, "missing id, url or date"})
			continue
		}
		status := "published"
		switch str(m["status"]) {
		case "waiting":
			status = "pending"
		case "spam":
			status = "spam"
		}
		parent := str(m["pid"])
		r.Comments = append(r.Comments, Comment{
			SourceID: id, ParentSourceID: parent, Page: str(m["url"]), AuthorName: str(m["nick"]),
			Email: cleanEmail(str(m["mail"])), AuthorURL: str(m["link"]), BodyMD: cleanMarkdown(str(m["comment"])),
			Status: status, Pinned: boolean(m["sticky"]), CreatedAt: created,
		})
	}
	return r, nil
}

// parseTwikoo reads Twikoo's export: a JSON array, or one MongoDB style
// object per line in older versions. Bodies are sanitized HTML; uid, which
// can be the administrator's password hash, is never read.
func parseTwikoo(data []byte) (Result, error) {
	var items []map[string]any
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return Result{}, ErrFormat
		}
	} else {
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				return Result{}, ErrFormat
			}
			items = append(items, m)
		}
	}
	var r Result
	for i, m := range items {
		id := str(m["_id"])
		page := str(m["href"])
		if page == "" {
			page = str(m["url"])
		}
		created, ok := parseTime(m["created"])
		if id == "" || page == "" || !ok {
			r.Failures = append(r.Failures, Failure{i, "missing _id, url or created"})
			continue
		}
		status := "published"
		if boolean(m["isSpam"]) {
			status = "spam"
		}
		r.Comments = append(r.Comments, Comment{
			SourceID: id, ParentSourceID: str(m["pid"]), Page: page, AuthorName: str(m["nick"]),
			Email: cleanEmail(str(m["mail"])), AuthorURL: str(m["link"]), BodyMD: HTMLToMarkdown(str(m["comment"])),
			Status: status, Pinned: boolean(m["top"]), CreatedAt: created,
		})
	}
	return r, nil
}

// parseArtrans reads Artalk's interchange format, an array of objects whose
// fields are all strings.
func parseArtrans(data []byte) (Result, error) {
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return Result{}, ErrFormat
	}
	var r Result
	for i, m := range items {
		id := str(m["id"])
		created, ok := parseTime(m["date"])
		if !ok {
			created, ok = parseTime(m["created_at"])
		}
		page := str(m["page_key"])
		if id == "" || page == "" || !ok {
			r.Failures = append(r.Failures, Failure{i, "missing id, page_key or date"})
			continue
		}
		if !strings.HasPrefix(page, "http") && !strings.HasPrefix(page, "/") {
			page = "/" + page
		}
		parent := str(m["rid"])
		if parent == "0" {
			parent = ""
		}
		status := "published"
		if boolean(m["is_pending"]) {
			status = "pending"
		}
		r.Comments = append(r.Comments, Comment{
			SourceID: id, ParentSourceID: parent, Page: page, PageTitle: str(m["page_title"]), AuthorName: str(m["nick"]),
			Email: cleanEmail(str(m["email"])), AuthorURL: str(m["link"]), BodyMD: cleanMarkdown(str(m["content"])),
			Status: status, Pinned: boolean(m["is_pinned"]), CreatedAt: created,
		})
	}
	return r, nil
}

// parseWXR reads WordPress's export. Only ordinary comments come in;
// pingbacks, trackbacks and notes are skipped, and trashed comments come
// in deleted (sites.md section 7, step 4).
func parseWXR(data []byte) (Result, error) {
	var f struct {
		XMLName xml.Name `xml:"rss"`
		Items   []struct {
			Title    string `xml:"title"`
			Link     string `xml:"link"`
			PostID   string `xml:"post_id"`
			Comments []struct {
				ID          string `xml:"comment_id"`
				Author      string `xml:"comment_author"`
				AuthorEmail string `xml:"comment_author_email"`
				AuthorURL   string `xml:"comment_author_url"`
				DateGMT     string `xml:"comment_date_gmt"`
				Date        string `xml:"comment_date"`
				Content     string `xml:"comment_content"`
				Approved    string `xml:"comment_approved"`
				Type        string `xml:"comment_type"`
				Parent      string `xml:"comment_parent"`
			} `xml:"comment"`
		} `xml:"channel>item"`
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&f); err != nil {
		return Result{}, ErrFormat
	}
	var r Result
	n := 0
	for _, it := range f.Items {
		for _, c := range it.Comments {
			n++
			if c.Type != "" && c.Type != "comment" {
				continue
			}
			created, ok := parseTime(c.DateGMT)
			if !ok || created.Year() < 1971 {
				created, ok = parseTime(c.Date)
			}
			if c.ID == "" || it.Link == "" || !ok {
				r.Failures = append(r.Failures, Failure{n - 1, "missing comment_id, link or date"})
				continue
			}
			status := "published"
			switch c.Approved {
			case "0":
				status = "pending"
			case "spam":
				status = "spam"
			case "trash", "post-trashed":
				status = "deleted"
			}
			parent := c.Parent
			if parent == "0" {
				parent = ""
			}
			r.Comments = append(r.Comments, Comment{
				SourceID: it.PostID + ":" + c.ID, ParentSourceID: prefixed(it.PostID, parent), Page: it.Link, PageTitle: it.Title,
				AuthorName: c.Author, Email: cleanEmail(c.AuthorEmail), AuthorURL: c.AuthorURL,
				BodyMD: HTMLToMarkdown(c.Content), Status: status, CreatedAt: created, PostID: it.PostID,
			})
		}
	}
	return r, nil
}

func prefixed(post, id string) string {
	if id == "" {
		return ""
	}
	return post + ":" + id
}
