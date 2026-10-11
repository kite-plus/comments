package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The parsers stream: each reads one comment at a time and stops at the
// comment cap, so a large file cannot blow up memory as a whole document
// tree would.

// counter enforces the comment cap.
type counter struct{ n, max int }

func (c *counter) add() error {
	c.n++
	if c.n > c.max {
		return ErrTooMany
	}
	return nil
}

// eachElement calls fn for every element of the JSON array that comes next.
func eachElement(dec *json.Decoder, fn func() error) error {
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return ErrFormat
	}
	for dec.More() {
		if err := fn(); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// eachKey calls fn with every key of the JSON object that comes next; fn
// must consume the key's value.
func eachKey(dec *json.Decoder, fn func(key string) error) error {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ErrFormat
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return ErrFormat
		}
		if err := fn(key); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// skip consumes the next value without keeping it, as for Waline's Users
// table, which is never read.
func skip(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('['), json.Delim('{'):
			depth++
		case json.Delim(']'), json.Delim('}'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func formatErr(err error) error {
	if errors.Is(err, ErrTooMany) {
		return err
	}
	return ErrFormat
}

type ownComment struct {
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
}

type ownThread struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	PublishedAt *time.Time `json:"published_at"`
	Locked      bool       `json:"locked"`
}

// parseOwn reads this service's export (docs/export-format.md). Readers'
// comments come in as external authors: their accounts stay with the
// instance they were written on (sites.md section 7). Keys come in any
// order, so comments wait until the threads are known.
func parseOwn(r io.Reader, max int) (Result, error) {
	dec := json.NewDecoder(r)
	var format string
	var version int
	var threads []ownThread
	var comments []ownComment
	cnt := &counter{max: max}
	err := eachKey(dec, func(key string) error {
		switch key {
		case "format":
			return dec.Decode(&format)
		case "version":
			return dec.Decode(&version)
		case "threads":
			return eachElement(dec, func() error {
				var t ownThread
				if len(threads) >= max {
					return ErrTooMany
				}
				threads = append(threads, t)
				return dec.Decode(&threads[len(threads)-1])
			})
		case "comments":
			return eachElement(dec, func() error {
				if err := cnt.add(); err != nil {
					return err
				}
				var c ownComment
				if err := dec.Decode(&c); err != nil {
					return err
				}
				comments = append(comments, c)
				return nil
			})
		}
		return skip(dec)
	})
	if err != nil || format != "kite-comments" {
		if err == nil {
			err = ErrFormat
		}
		return Result{}, formatErr(err)
	}
	if version != 1 {
		return Result{}, fmt.Errorf("export version %d is not supported", version)
	}
	byID := map[string]ownThread{}
	for _, t := range threads {
		byID[t.ID] = t
	}
	var res Result
	for i, c := range comments {
		t, ok := byID[c.ThreadID]
		if !ok || c.ID == "" {
			res.Failures = append(res.Failures, Failure{i, "unknown thread or missing id"})
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
		res.Comments = append(res.Comments, Comment{
			SourceID: c.ID, ParentSourceID: c.ParentID, Page: t.URL, PageTitle: t.Title, ThreadKey: t.Key,
			AuthorName: name, Email: email, AuthorURL: c.Author.URL, AuthorRef: c.Author.Ref, BodyMD: c.Body.Source,
			Status: normalStatus(c.Status), Pinned: c.Pinned, CreatedAt: c.CreatedAt, EditedAt: c.EditedAt,
			ThreadLock: t.Locked, PublishedAt: t.PublishedAt,
		})
	}
	return res, nil
}

func normalStatus(s string) string {
	switch s {
	case "published", "pending", "spam", "hidden", "deleted":
		return s
	}
	return "published"
}

// parseWaline reads Waline's admin export: {type: "waline", version: 1,
// data: {Comment: [...]}}. Only the Comment table is read; the Users table,
// with password hashes and two-factor secrets, is skipped unread.
func parseWaline(r io.Reader, max int) (Result, error) {
	dec := json.NewDecoder(r)
	var typ string
	var res Result
	cnt := &counter{max: max}
	err := eachKey(dec, func(key string) error {
		switch key {
		case "type":
			return dec.Decode(&typ)
		case "data":
			return eachKey(dec, func(table string) error {
				if table != "Comment" {
					return skip(dec)
				}
				i := -1
				return eachElement(dec, func() error {
					i++
					if err := cnt.add(); err != nil {
						return err
					}
					var m map[string]any
					if err := dec.Decode(&m); err != nil {
						return err
					}
					walineComment(&res, i, m)
					return nil
				})
			})
		}
		return skip(dec)
	})
	if err != nil || typ != "waline" {
		if err == nil {
			err = ErrFormat
		}
		return Result{}, formatErr(err)
	}
	return res, nil
}

func walineComment(res *Result, i int, m map[string]any) {
	id := str(m["objectId"])
	if id == "" {
		id = str(m["id"])
	}
	created, ok := parseTime(m["insertedAt"])
	if !ok {
		created, ok = parseTime(m["createdAt"])
	}
	if id == "" || str(m["url"]) == "" || !ok {
		res.Failures = append(res.Failures, Failure{i, "missing id, url or date"})
		return
	}
	status := "published"
	switch str(m["status"]) {
	case "waiting":
		status = "pending"
	case "spam":
		status = "spam"
	}
	res.Comments = append(res.Comments, Comment{
		SourceID: id, ParentSourceID: str(m["pid"]), Page: str(m["url"]), AuthorName: str(m["nick"]),
		Email: cleanEmail(str(m["mail"])), AuthorURL: str(m["link"]), BodyMD: cleanMarkdown(str(m["comment"])),
		Status: status, Pinned: boolean(m["sticky"]), CreatedAt: created,
	})
}

// parseTwikoo reads Twikoo's export: a JSON array, or one MongoDB style
// object per line in older versions. Bodies are sanitized HTML; uid, which
// can be the administrator's password hash, is never read.
func parseTwikoo(r io.Reader, max int) (Result, error) {
	br := bufio.NewReader(r)
	first, err := firstByte(br)
	if err != nil {
		return Result{}, ErrFormat
	}
	var res Result
	cnt := &counter{max: max}
	i := -1
	one := func(m map[string]any) error {
		i++
		if err := cnt.add(); err != nil {
			return err
		}
		twikooComment(&res, i, m)
		return nil
	}
	if first == '[' {
		dec := json.NewDecoder(br)
		err := eachElement(dec, func() error {
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return err
			}
			return one(m)
		})
		if err != nil {
			return Result{}, formatErr(err)
		}
		return res, nil
	}
	sc := bufio.NewScanner(br)
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
		if err := one(m); err != nil {
			return Result{}, err
		}
	}
	if sc.Err() != nil {
		return Result{}, ErrFormat
	}
	return res, nil
}

func firstByte(br *bufio.Reader) (byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return b, br.UnreadByte()
		}
	}
}

func twikooComment(res *Result, i int, m map[string]any) {
	id := str(m["_id"])
	page := str(m["href"])
	if page == "" {
		page = str(m["url"])
	}
	created, ok := parseTime(m["created"])
	if id == "" || page == "" || !ok {
		res.Failures = append(res.Failures, Failure{i, "missing _id, url or created"})
		return
	}
	status := "published"
	if boolean(m["isSpam"]) {
		status = "spam"
	}
	res.Comments = append(res.Comments, Comment{
		SourceID: id, ParentSourceID: str(m["pid"]), Page: page, AuthorName: str(m["nick"]),
		Email: cleanEmail(str(m["mail"])), AuthorURL: str(m["link"]), BodyMD: HTMLToMarkdown(str(m["comment"])),
		Status: status, Pinned: boolean(m["top"]), CreatedAt: created,
	})
}

// parseArtrans reads Artalk's interchange format, an array of objects whose
// fields are all strings.
func parseArtrans(r io.Reader, max int) (Result, error) {
	dec := json.NewDecoder(r)
	var res Result
	cnt := &counter{max: max}
	i := -1
	err := eachElement(dec, func() error {
		i++
		if err := cnt.add(); err != nil {
			return err
		}
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return err
		}
		artransComment(&res, i, m)
		return nil
	})
	if err != nil {
		return Result{}, formatErr(err)
	}
	return res, nil
}

func artransComment(res *Result, i int, m map[string]any) {
	id := str(m["id"])
	created, ok := parseTime(m["date"])
	if !ok {
		created, ok = parseTime(m["created_at"])
	}
	page := str(m["page_key"])
	if id == "" || page == "" || !ok {
		res.Failures = append(res.Failures, Failure{i, "missing id, page_key or date"})
		return
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
	res.Comments = append(res.Comments, Comment{
		SourceID: id, ParentSourceID: parent, Page: page, PageTitle: str(m["page_title"]), AuthorName: str(m["nick"]),
		Email: cleanEmail(str(m["email"])), AuthorURL: str(m["link"]), BodyMD: cleanMarkdown(str(m["content"])),
		Status: status, Pinned: boolean(m["is_pinned"]), CreatedAt: created,
	})
}

type wxrItem struct {
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
}

// parseWXR reads WordPress's export one item at a time. Only ordinary
// comments come in; pingbacks, trackbacks and notes are skipped, and
// trashed comments come in deleted (sites.md section 7, step 4).
func parseWXR(r io.Reader, max int) (Result, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var res Result
	cnt := &counter{max: max}
	n, sawRSS := 0, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, ErrFormat
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "rss" {
			sawRSS = true
		}
		if start.Name.Local != "item" {
			continue
		}
		var it wxrItem
		if err := dec.DecodeElement(&it, &start); err != nil {
			return Result{}, ErrFormat
		}
		for _, c := range it.Comments {
			n++
			if err := cnt.add(); err != nil {
				return Result{}, err
			}
			if c.Type != "" && c.Type != "comment" {
				continue
			}
			created, ok := parseTime(c.DateGMT)
			if !ok || created.Year() < 1971 {
				created, ok = parseTime(c.Date)
			}
			if c.ID == "" || it.Link == "" || !ok {
				res.Failures = append(res.Failures, Failure{n - 1, "missing comment_id, link or date"})
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
			res.Comments = append(res.Comments, Comment{
				SourceID: it.PostID + ":" + c.ID, ParentSourceID: prefixed(it.PostID, parent), Page: it.Link, PageTitle: it.Title,
				AuthorName: c.Author, Email: cleanEmail(c.AuthorEmail), AuthorURL: c.AuthorURL,
				BodyMD: HTMLToMarkdown(c.Content), Status: status, CreatedAt: created, PostID: it.PostID,
			})
		}
	}
	if !sawRSS {
		return Result{}, ErrFormat
	}
	return res, nil
}

func prefixed(post, id string) string {
	if id == "" {
		return ""
	}
	return post + ":" + id
}
