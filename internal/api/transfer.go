package api

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/importer"
	"github.com/kite-plus/comments/internal/markup"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/store"
)

// importSite describes the site for import planning: only its verified
// origins count as its own.
func importSite(site model.Site) importer.Site {
	return importer.Site{Origins: site.VerifiedOrigins(), Primary: site.PrimaryOrigin(), QueryKeys: site.QueryKeys}
}

func importJSON(im store.Import) gin.H {
	return gin.H{"id": strconv.FormatInt(im.ID, 10), "format": im.Format, "status": im.Status,
		"mapping": json.RawMessage(im.Mapping), "stats": json.RawMessage(im.Stats), "error": im.Error,
		"created_at": im.CreatedAt.UTC(), "finished_at": timeOrNil(im.FinishedAt)}
}

// previewImport plans an import's stored comments with a mapping and
// stores the summary.
func (s *Server) previewImport(c *gin.Context, site model.Site, id int64, m importer.Mapping) bool {
	stored, err := s.Store.ImportFile(c.Request.Context(), id)
	if err != nil {
		s.storeError(c, err)
		return false
	}
	var r importer.Result
	if err := json.Unmarshal(stored, &r); err != nil {
		s.storeError(c, err)
		return false
	}
	mapping, _ := json.Marshal(m)
	_, sum := importer.MakePlan(r, m, importSite(site))
	stats, _ := json.Marshal(gin.H{"preview": sum})
	if err := s.Store.SetImportPreview(c.Request.Context(), id, mapping, stats, ""); err != nil {
		s.storeError(c, err)
		return false
	}
	return true
}

// importable reports whether a site may import: only approved or active
// sites, so an unvetted account cannot fill the database.
func importable(site model.Site) bool {
	return site.Status == model.SiteActive || (site.ApprovedAt != nil && site.Status == model.SiteUnverified)
}

// uploadImport is POST /api/v1/sites/{site}/imports (multipart: format,
// file). The file is parsed at once, streaming and up to the comment cap;
// what is stored is the parsed comments with guest addresses encrypted,
// never the file (sites.md section 7). The response is the preview.
func (s *Server) uploadImport(c *gin.Context) {
	site := currentSite(c)
	if !importable(site) {
		s.fail(c, http.StatusForbidden, codeSiteInactive)
		return
	}
	format := c.PostForm("format")
	if !slices.Contains(importer.Formats, format) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if ok, retry := s.uploads.allow(site.PublicID); !ok {
		s.tooMany(c, retry)
		return
	}
	file, ok := readUpload(c)
	if !ok {
		s.fail(c, http.StatusBadRequest, codeImportInvalid)
		return
	}
	r, err := importer.Parse(format, file)
	if err != nil {
		s.fail(c, http.StatusUnprocessableEntity, codeImportInvalid, importDetail(c, err))
		return
	}
	n, err := s.Store.ImportedCount(c.Request.Context(), site.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if n+len(r.Comments) > policy.ImportedPerSite {
		s.fail(c, http.StatusUnprocessableEntity, codeImportInvalid, t(c, "站点的导入评论超出配额。", "The site would exceed its quota of imported comments."))
		return
	}
	for i := range r.Comments {
		if e := r.Comments[i].Email; e != "" {
			r.Comments[i].EmailEnc, r.Comments[i].EmailHMAC = s.Keys.Encrypt(crypt.NormalizeEmail(e)), s.Keys.EmailHMAC(e)
			r.Comments[i].Email = ""
		}
	}
	parsed, _ := json.Marshal(r)
	id, err := s.Store.CreateImport(c.Request.Context(), site.ID, format, parsed, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if !s.previewImport(c, site, id, importer.Mapping{}) {
		return
	}
	im, err := s.Store.Import(c.Request.Context(), site.ID, id)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusCreated, importJSON(im))
}

func importDetail(c *gin.Context, err error) string {
	if errors.Is(err, importer.ErrTooMany) {
		return t(c, fmt.Sprintf("一次最多导入 %d 条评论。", policy.ImportMaxComments),
			fmt.Sprintf("An import holds at most %d comments.", policy.ImportMaxComments))
	}
	return ""
}

func (s *Server) currentImport(c *gin.Context) (store.Import, bool) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return store.Import{}, false
	}
	im, err := s.Store.Import(c.Request.Context(), currentSite(c).ID, id)
	if err != nil {
		s.storeError(c, err)
		return im, false
	}
	return im, true
}

// getImport is GET /api/v1/sites/{site}/imports/{id}.
func (s *Server) getImport(c *gin.Context) {
	im, ok := s.currentImport(c)
	if ok {
		private(c, http.StatusOK, importJSON(im))
	}
}

// setImportMapping is PUT /api/v1/sites/{site}/imports/{id}/mapping; the
// preview is made again with it.
func (s *Server) setImportMapping(c *gin.Context) {
	im, ok := s.currentImport(c)
	if !ok {
		return
	}
	var m importer.Mapping
	if !s.bindJSON(c, &m) {
		return
	}
	if m.WordPressKeys != "" && m.WordPressKeys != "url" && m.WordPressKeys != "post_id" || len(m.Rewrites) > 20 {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if m.Origin != "" {
		o, ok := s.siteOrigin(m.Origin)
		if !ok {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		m.Origin = o
	}
	if im.Status != "uploaded" && im.Status != "previewed" {
		s.fail(c, http.StatusConflict, codeConflict)
		return
	}
	if !s.previewImport(c, currentSite(c), im.ID, m) {
		return
	}
	im, _ = s.Store.Import(c.Request.Context(), im.SiteID, im.ID)
	private(c, http.StatusOK, importJSON(im))
}

// runImport is POST /api/v1/sites/{site}/imports/{id}/run; the worker does
// the work.
func (s *Server) runImport(c *gin.Context) {
	im, ok := s.currentImport(c)
	if !ok {
		return
	}
	if err := s.Store.StartImport(c.Request.Context(), im.SiteID, im.ID); err != nil {
		s.fail(c, http.StatusConflict, codeConflict)
		return
	}
	im, _ = s.Store.Import(c.Request.Context(), im.SiteID, im.ID)
	private(c, http.StatusAccepted, importJSON(im))
}

// exportSite is GET /api/v1/sites/{site}/export?format=json|artrans
// (sites.md section 8; the JSON format is docs/export-format.md).
func (s *Server) exportSite(c *gin.Context) {
	site := currentSite(c)
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "artrans" && format != "wxr" {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	threads, comments, err := s.Store.ExportSite(c.Request.Context(), site.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	emails := map[int64]string{}
	for _, cm := range comments {
		if cm.EmailEnc != nil {
			if e, err := s.Keys.Decrypt(cm.EmailEnc); err == nil {
				emails[cm.ID] = e
			}
		}
	}
	name := "kite-comments-" + site.PublicID[:8] + "-" + s.Now().UTC().Format("20060102")
	if format == "wxr" {
		c.Header("Content-Disposition", `attachment; filename="`+name+`.wxr.xml"`)
		c.Header("Cache-Control", "private, no-store")
		c.Data(http.StatusOK, "application/xml; charset=utf-8", exportWXR(site, threads, comments, emails))
		return
	}
	var body any
	if format == "json" {
		body = exportJSON(site, threads, comments, emails, s.Now())
		name += ".json"
	} else {
		body = exportArtrans(site, threads, comments, emails)
		name += ".artrans.json"
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	private(c, http.StatusOK, body)
}

func exportJSON(site model.Site, threads []store.ExportThread, comments []store.ExportComment, emails map[int64]string, now time.Time) gin.H {
	ts := make([]gin.H, 0, len(threads))
	for _, t := range threads {
		ts = append(ts, gin.H{"id": strconv.FormatInt(t.ID, 10), "key": t.Key, "url": t.URL, "title": t.Title,
			"published_at": timeOrNil(t.PagePublishedAt), "locked": t.LockedAt != nil, "created_at": t.CreatedAt.UTC()})
	}
	cs := make([]gin.H, 0, len(comments))
	for _, cm := range comments {
		author := gin.H{"kind": cm.AuthorKind, "name": cm.AuthorName}
		if cm.AuthorKind == model.AuthorUser {
			author["id"] = cm.AuthorID
		}
		if cm.AuthorURL != "" {
			author["url"] = cm.AuthorURL
		}
		if cm.AuthorRef != "" {
			author["ref"] = cm.AuthorRef
		}
		if e, ok := emails[cm.ID]; ok {
			author["email"] = e
		}
		var source any
		if cm.Source != "native" {
			source = gin.H{"system": cm.Source, "id": cm.SourceID}
		}
		cs = append(cs, gin.H{"id": strconv.FormatInt(cm.ID, 10), "thread_id": strconv.FormatInt(cm.ThreadID, 10),
			"parent_id": idString(cm.ParentID), "root_id": idString(cm.RootID), "author": author,
			"body": gin.H{"format": "markdown", "source": cm.BodyMD}, "status": cm.Status, "pinned": cm.Pinned,
			"created_at": cm.CreatedAt.UTC(), "edited_at": timeOrNil(cm.EditedAt), "published_at": timeOrNil(cm.PublishedAt),
			"source": source})
	}
	origins := []string{}
	for _, o := range site.Origins {
		origins = append(origins, o.Origin)
	}
	return gin.H{"format": "kite-comments", "version": 1, "exported_at": now.UTC(),
		"site": gin.H{"id": site.PublicID, "name": site.Name, "origins": origins}, "threads": ts, "comments": cs}
}

// exportArtrans writes Artalk's interchange format: an array of objects
// whose fields are all strings. It has no version or formal spec.
func exportArtrans(site model.Site, threads []store.ExportThread, comments []store.ExportComment, emails map[int64]string) []map[string]string {
	byID := map[int64]store.ExportThread{}
	for _, t := range threads {
		byID[t.ID] = t
	}
	var urls []string
	for _, o := range site.Origins {
		urls = append(urls, o.Origin)
	}
	out := []map[string]string{}
	for _, cm := range comments {
		if cm.Status == model.StatusDeleted || cm.Status == model.StatusSpam {
			continue
		}
		t := byID[cm.ThreadID]
		rid := "0"
		if cm.ParentID != nil {
			rid = strconv.FormatInt(*cm.ParentID, 10)
		}
		name := cm.AuthorName
		if name == "" {
			name = "匿名"
		}
		date := cm.CreatedAt.UTC().Format("2006-01-02 15:04:05")
		out = append(out, map[string]string{
			"id": strconv.FormatInt(cm.ID, 10), "rid": rid, "content": cm.BodyMD, "ua": "", "ip": "",
			"is_collapsed": "false", "is_pending": strconv.FormatBool(cm.Status != model.StatusPublished),
			"is_pinned": strconv.FormatBool(cm.Pinned), "vote_up": "0", "vote_down": "0",
			"date": date, "created_at": date, "updated_at": date,
			"nick": name, "email": emails[cm.ID], "link": cm.AuthorURL, "password": "", "badge_name": "", "badge_color": "",
			"page_key": t.URL, "page_title": t.Title, "page_admin_only": "false",
			"site_name": site.Name, "site_urls": strings.Join(urls, ","),
		})
	}
	return out
}

// exportWXR writes WordPress's export format, which WordPress and Disqus
// import (sites.md section 8): one item per thread with its comments as
// HTML. Spam and deleted comments go as spam and trash; readers' email
// addresses are never included.
func exportWXR(site model.Site, threads []store.ExportThread, comments []store.ExportComment, emails map[int64]string) []byte {
	byThread := map[int64][]store.ExportComment{}
	for _, cm := range comments {
		byThread[cm.ThreadID] = append(byThread[cm.ThreadID], cm)
	}
	var b strings.Builder
	x := func(s string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(s))
		return out.String()
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">` + "\n")
	b.WriteString("<channel>\n<title>" + x(site.Name) + "</title>\n<link>" + x(site.PrimaryOrigin()) + "</link>\n<wp:wxr_version>1.2</wp:wxr_version>\n")
	for _, t := range threads {
		status := "open"
		if t.LockedAt != nil {
			status = "closed"
		}
		date := t.CreatedAt
		if t.PagePublishedAt != nil {
			date = *t.PagePublishedAt
		}
		b.WriteString("<item>\n<title>" + x(t.Title) + "</title>\n<link>" + x(t.URL) + "</link>\n")
		b.WriteString("<wp:post_id>" + strconv.FormatInt(t.ID, 10) + "</wp:post_id>\n<wp:post_name>" + x(t.Key) + "</wp:post_name>\n")
		b.WriteString("<wp:post_date_gmt>" + date.UTC().Format("2006-01-02 15:04:05") + "</wp:post_date_gmt>\n")
		b.WriteString("<wp:post_type>post</wp:post_type>\n<wp:status>publish</wp:status>\n<wp:comment_status>" + status + "</wp:comment_status>\n")
		for _, cm := range byThread[t.ID] {
			approved := "1"
			switch cm.Status {
			case model.StatusPending, model.StatusHidden:
				approved = "0"
			case model.StatusSpam:
				approved = "spam"
			case model.StatusDeleted:
				approved = "trash"
			}
			parent := "0"
			if cm.ParentID != nil {
				parent = strconv.FormatInt(*cm.ParentID, 10)
			}
			when := cm.CreatedAt.UTC().Format("2006-01-02 15:04:05")
			b.WriteString("<wp:comment>\n<wp:comment_id>" + strconv.FormatInt(cm.ID, 10) + "</wp:comment_id>\n")
			b.WriteString("<wp:comment_author>" + x(cm.AuthorName) + "</wp:comment_author>\n")
			b.WriteString("<wp:comment_author_email>" + x(emails[cm.ID]) + "</wp:comment_author_email>\n")
			b.WriteString("<wp:comment_author_url>" + x(cm.AuthorURL) + "</wp:comment_author_url>\n<wp:comment_author_IP></wp:comment_author_IP>\n")
			b.WriteString("<wp:comment_date>" + when + "</wp:comment_date>\n<wp:comment_date_gmt>" + when + "</wp:comment_date_gmt>\n")
			b.WriteString("<wp:comment_content>" + x(markup.Render(cm.BodyMD).HTML) + "</wp:comment_content>\n")
			b.WriteString("<wp:comment_approved>" + approved + "</wp:comment_approved>\n<wp:comment_type>comment</wp:comment_type>\n")
			b.WriteString("<wp:comment_parent>" + parent + "</wp:comment_parent>\n<wp:comment_user_id>0</wp:comment_user_id>\n</wp:comment>\n")
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel>\n</rss>\n")
	return []byte(b.String())
}
