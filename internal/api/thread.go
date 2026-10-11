package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/pagekey"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/store"
)

// Modes of the iframe document (widget.md section 4).
const (
	modeNormal   = "normal"   // a verified origin: reads and writes
	modePreview  = "preview"  // http://localhost or 127.0.0.1: read only
	modeForeign  = "foreign"  // the page is not on the site
	modeClosed   = "closed"   // the site is suspended: read only
	modeInactive = "inactive" // unverified, in review, deleted or unknown
)

var accentRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// pageInfo is a page as the widget describes it.
type pageInfo struct {
	Key   string
	URL   string
	Mode  string
	Valid bool
}

// resolvePage works out a page's identifier and the document mode from the
// site and the page address.
func (s *Server) resolvePage(site model.Site, rawURL, explicitKey string) pageInfo {
	key, origin, err := pagekey.FromURL(rawURL, site.QueryKeys)
	if err != nil {
		return pageInfo{Mode: modeForeign}
	}
	p := pageInfo{Key: key, URL: rawURL, Valid: true}
	if explicitKey != "" {
		if p.Key, err = pagekey.Explicit(explicitKey); err != nil {
			return pageInfo{Mode: modeForeign}
		}
	}
	switch {
	case site.HasVerifiedOrigin(origin) && (strings.HasPrefix(origin, "https://") || s.DevHTTPOrigins):
		p.Mode = modeNormal
	case site.AllowLocalhost && isLocalPreview(origin):
		p.Mode = modePreview
	default:
		p.Mode, p.Valid = modeForeign, false
	}
	return p
}

func isLocalPreview(origin string) bool {
	return origin == "http://localhost" || strings.HasPrefix(origin, "http://localhost:") ||
		origin == "http://127.0.0.1" || strings.HasPrefix(origin, "http://127.0.0.1:")
}

// frameAncestors is the CSP directive that keeps the widget on verified
// sites (widget.md section 4, item 4).
func (s *Server) frameAncestors(site model.Site, mode string) string {
	if mode == modePreview {
		return "frame-ancestors http://localhost:* http://127.0.0.1:*"
	}
	var list []string
	for _, o := range site.VerifiedOrigins() {
		if strings.HasPrefix(o, "https://") || s.DevHTTPOrigins {
			list = append(list, o)
		}
	}
	if len(list) == 0 {
		return "frame-ancestors 'none'"
	}
	return "frame-ancestors " + strings.Join(list, " ")
}

func (s *Server) threadCSP(site model.Site, mode string) string {
	style := "'self'"
	if s.assets.cssHash != "" {
		style += " " + s.assets.cssHash
	}
	form := "'self'"
	if s.issuerOrg != "" {
		// The redirect login posts to /auth/redirect, which redirects to
		// the identity service; browsers check form-action on redirects.
		form += " " + s.issuerOrg
	}
	return "default-src 'none'; script-src 'self'; style-src " + style + "; img-src 'self' data:; connect-src 'self'; " +
		"form-action " + form + "; base-uri 'none'; " + s.frameAncestors(site, mode) +
		"; require-trusted-types-for 'script'; trusted-types kc"
}

// threadDocument is GET /embed/v1/thread, the iframe: a shell with the
// first page inlined. It reads no session and creates nothing.
func (s *Server) threadDocument(c *gin.Context) {
	ctx := c.Request.Context()
	lang := c.Query("lang")
	if lang != model.LocaleEN {
		lang = model.LocaleZH
	}
	theme := c.Query("theme")
	if theme != "dark" {
		theme = "light"
	}
	accent := c.Query("accent")
	if !accentRE.MatchString(accent) {
		accent = "#3d65bd"
	}
	font := c.Query("font")
	if font != "serif" {
		font = "sans"
	}
	data := map[string]any{
		"v": 1, "public_url": s.origin, "lang": lang, "theme": theme, "accent": accent, "font": font,
		"color_scheme": colorScheme(c.Query("cs"), theme), "mode": modeInactive, "origins": []string{},
	}
	site, err := s.Store.Site(ctx, c.Query("site"))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.Log.Error("thread document", "error", err)
	}
	mode := modeInactive
	if err == nil {
		data["site"] = site.PublicID
		data["origins"] = nonNil(site.VerifiedOrigins())
		data["guest_policy"] = site.GuestPolicy
		data["sort"] = site.SortOrder
		if site.Status == model.SiteActive || site.Status == model.SiteSuspended {
			page := s.resolvePage(site, c.Query("url"), c.Query("key"))
			mode = page.Mode
			if site.Status == model.SiteSuspended && page.Valid {
				mode = modeClosed
			}
			if page.Valid {
				data["key"] = page.Key
				data["url"] = page.URL
				data["title"] = truncate(c.Query("title"), policy.ThreadTitleRunes)
				if p := c.Query("published"); p != "" {
					data["published"] = p
				}
				var focus int64
				if f := c.Query("focus"); f != "" {
					focus, _ = strconv.ParseInt(f, 10, 64)
					data["focus"] = strconv.FormatInt(focus, 10)
				}
				body, err := s.firstPage(c, site, page.Key, site.SortOrder == model.SortNewest, focus)
				if err != nil {
					s.Log.Error("thread document", "error", err)
					mode = modeInactive
				} else {
					for k, v := range body {
						data[k] = v
					}
				}
			}
		}
		c.Header("Content-Security-Policy", s.threadCSP(site, mode))
	} else {
		c.Header("Content-Security-Policy", s.threadCSP(model.Site{}, mode))
	}
	data["mode"] = mode
	c.Header("Cache-Control", "public, max-age=30")
	c.Data(http.StatusOK, "text/html; charset=utf-8", s.renderShell(lang, theme, data))
}

func colorScheme(v, theme string) string {
	if v == "light" || v == "dark" || v == "light dark" {
		return v
	}
	if theme == "dark" {
		return "dark"
	}
	return "light"
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) renderShell(lang, theme string, data map[string]any) []byte {
	raw, _ := json.Marshal(data) // escapes <, > and &, so it cannot end the script element
	title := "评论"
	if lang == model.LocaleEN {
		title = "Comments"
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + lang + `" data-theme="` + theme + `"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer">`)
	b.WriteString(`<title>` + html.EscapeString(title) + `</title>`)
	if s.assets.appCSS != "" {
		b.WriteString(`<style>` + s.assets.appCSS + `</style>`)
	}
	b.WriteString(`<script type="application/json" id="kc-data">`)
	b.Write(raw)
	b.WriteString(`</script>`)
	if s.assets.ok {
		b.WriteString(`<script type="module" src="/static/widget/` + s.assets.appJS + `"></script>`)
	}
	b.WriteString(`</head><body><div id="kc-root">`)
	if !s.assets.ok {
		b.WriteString(`<p>The comment widget is not built in this binary.</p>`)
	}
	b.WriteString(`</div></body></html>`)
	return []byte(b.String())
}

// firstPage is the inlined first page: the same body as the comments API.
func (s *Server) firstPage(c *gin.Context, site model.Site, key string, newest bool, around int64) (gin.H, error) {
	return s.commentsBody(c, site, key, newest, nil, policy.CommentsPage, around, nil, false)
}

// commentsBody builds the comments API body for a page key. A page with no
// thread yet is an empty list; nothing is created.
func (s *Server) commentsBody(c *gin.Context, site model.Site, key string, newest bool, cur *model.Cursor, limit int,
	around int64, statuses []string, manager bool) (gin.H, error) {
	ctx := c.Request.Context()
	t, err := s.Store.ThreadByKey(ctx, site.ID, key)
	if errors.Is(err, store.ErrNotFound) {
		closed := false
		return gin.H{"data": []any{}, "next_cursor": nil, "count": 0, "locked": false, "closed": closed, "partial": false}, nil
	}
	if err != nil {
		return nil, err
	}
	page, err := s.Store.ListComments(ctx, store.ListQuery{ThreadID: t.ID, Newest: newest, Cursor: cur, Limit: limit,
		Statuses: statuses, Around: around})
	if err != nil {
		return nil, err
	}
	list := make([]gin.H, 0, len(page.Comments))
	for _, cm := range page.Comments {
		list = append(list, s.commentJSON(site, cm, manager))
	}
	closesAt := t.ClosesAt(site.CloseAfterDays)
	return gin.H{
		"data": list, "next_cursor": encodeCursor(page.Next), "count": t.CommentCount,
		"locked": t.LockedAt != nil, "closed": closesAt != nil && s.Now().After(*closesAt),
		"partial": page.Partial, "thread_id": strconv.FormatInt(t.ID, 10),
	}, nil
}

// commentJSON is the comment object of api.md section 1. Managers also see
// the status; a top-level comment that is gone but has visible replies is
// a placeholder.
func (s *Server) commentJSON(site model.Site, cm model.Comment, manager bool) gin.H {
	replies := make([]gin.H, 0, len(cm.Replies))
	for _, r := range cm.Replies {
		replies = append(replies, s.commentJSON(site, r, manager))
	}
	out := gin.H{
		"id": strconv.FormatInt(cm.ID, 10), "root_id": idString(cm.RootID), "parent_id": idString(cm.ParentID),
		"created_at": cm.CreatedAt.UTC(),
	}
	if cm.RootID == nil {
		out["reply_count"] = cm.ReplyCount
		out["replies"] = replies
		// Where GET /api/v1/comments/{id}/replies continues after the
		// replies shown inline.
		out["replies_cursor"] = nil
		if n := len(cm.Replies); n > 0 {
			last := cm.Replies[n-1]
			out["replies_cursor"] = encodeCursor(&model.Cursor{At: last.CreatedAt, ID: last.ID})
		}
	}
	visible := cm.Status == model.StatusPublished || (manager && cm.Status != model.StatusDeleted)
	if !visible {
		out["placeholder"] = "hidden"
		if cm.Status == model.StatusDeleted {
			out["placeholder"] = "deleted"
		}
		if manager {
			out["status"] = cm.Status
		}
		return out
	}
	var reply any
	if cm.ReplyTo != nil {
		reply = gin.H{"id": strconv.FormatInt(cm.ReplyTo.ID, 10), "name": cm.ReplyTo.Name}
	}
	out["reply_to"] = reply
	out["author"] = s.authorJSON(site, cm)
	out["body_html"] = cm.BodyHTML
	out["pinned"] = cm.PinnedAt != nil
	out["edited_at"] = timeOrNil(cm.EditedAt)
	if manager {
		out["status"] = cm.Status
	}
	return out
}

func (s *Server) authorJSON(site model.Site, cm model.Comment) gin.H {
	a := gin.H{"kind": cm.AuthorKind, "name": cm.AuthorName}
	if cm.AuthorKind == model.AuthorFormerUser {
		a["name"] = ""
	}
	if cm.AuthorURL != "" && cm.AuthorKind != model.AuthorFormerUser {
		a["url"] = cm.AuthorURL
	}
	if cm.AuthorKind == model.AuthorUser {
		a["id"] = cm.AuthorPublicID
		if cm.Badge != "" {
			a["badge"] = cm.Badge
		}
	}
	a["avatar"] = avatarURL(site, cm)
	return a
}

func avatarURL(site model.Site, cm model.Comment) string {
	if cm.AuthorKind == model.AuthorUser && cm.AuthorHasAvatar && cm.AuthorPublicID != "" {
		return "/avatars/" + cm.AuthorPublicID + "?v=" + url.QueryEscape(cm.AuthorAvatarTag)
	}
	var seed string
	switch {
	case cm.AuthorKind == model.AuthorUser && cm.AuthorPublicID != "":
		seed = cm.AuthorPublicID
	case cm.AuthorKind == model.AuthorGuest && len(cm.AuthorEmailHMAC) > 0:
		// Per site, so a guest's avatar does not link their comments across sites.
		seed = shortHash(site.PublicID + ":g:" + hex.EncodeToString(cm.AuthorEmailHMAC))
	default:
		seed = shortHash(site.PublicID + ":n:" + cm.AuthorName + ":" + strconv.FormatInt(cm.ID, 10))
	}
	return "/avatars/generated/" + seed + ".svg"
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:10])
}

// readableSite loads a site whose comments may be read anonymously.
func (s *Server) readableSite(c *gin.Context, publicID string) (model.Site, bool) {
	site, err := s.Store.Site(c.Request.Context(), publicID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && site.Status != model.SiteActive && site.Status != model.SiteSuspended) {
		s.fail(c, http.StatusForbidden, codeSiteInactive)
		return site, false
	}
	if err != nil {
		s.storeError(c, err)
		return site, false
	}
	return site, true
}

// threadComments is GET /api/v1/threads/comments, the anonymous page read.
func (s *Server) threadComments(c *gin.Context) {
	site, ok := s.readableSite(c, c.Query("site"))
	if !ok {
		return
	}
	key := c.Query("key")
	if !pagekey.Valid(key) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	cur, limit, ok := s.page(c, policy.CommentsPage, policy.CommentsPageMax)
	if !ok {
		return
	}
	newest, ok := s.sortParam(c, site)
	if !ok {
		return
	}
	around, _ := strconv.ParseInt(c.Query("around"), 10, 64)
	body, err := s.commentsBody(c, site, key, newest, cur, limit, around, nil, false)
	if err != nil {
		s.storeError(c, err)
		return
	}
	c.Header("Vary", "Accept-Language")
	cached(c, 30*time.Second, "application/json; charset=utf-8", encodeJSON(body))
}

func (s *Server) sortParam(c *gin.Context, site model.Site) (bool, bool) {
	switch c.Query("sort") {
	case "":
		return site.SortOrder == model.SortNewest, true
	case model.SortNewest:
		return true, true
	case model.SortOldest:
		return false, true
	}
	s.fail(c, http.StatusBadRequest, codeInvalidRequest)
	return false, false
}

// replies is GET /api/v1/comments/{id}/replies.
func (s *Server) replies(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	root, err := s.Store.Comment(c.Request.Context(), id)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if root.RootID != nil {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	site, err := s.Store.SiteByID(c.Request.Context(), root.SiteID)
	if err != nil || (site.Status != model.SiteActive && site.Status != model.SiteSuspended) {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	cur, limit, ok := s.page(c, policy.CommentsPage, policy.CommentsPageMax)
	if !ok {
		return
	}
	list, next, err := s.Store.Replies(c.Request.Context(), id, nil, cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, r := range list {
		data = append(data, s.commentJSON(site, r, false))
	}
	cached(c, 30*time.Second, "application/json; charset=utf-8", encodeJSON(gin.H{"data": data, "next_cursor": encodeCursor(next)}))
}

// avatar serves /avatars/{author ID} and /avatars/generated/{seed}.svg.
func (s *Server) avatar(c *gin.Context) {
	p := strings.TrimPrefix(c.Param("path"), "/")
	if seed, ok := strings.CutPrefix(p, "generated/"); ok {
		seed, ok = strings.CutSuffix(seed, ".svg")
		if !ok || len(seed) < 4 || len(seed) > 64 || strings.ContainsFunc(seed, func(r rune) bool { return !isHexOrDash(r) }) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Data(http.StatusOK, "image/svg+xml", generatedAvatar(seed))
		return
	}
	a, err := s.Store.AvatarByAuthor(c.Request.Context(), p)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	maxAge := "public, max-age=3600"
	if c.Query("v") == a.ETag {
		maxAge = "public, max-age=31536000, immutable"
	}
	c.Header("Cache-Control", maxAge)
	c.Header("ETag", `"`+a.ETag+`"`)
	c.Data(http.StatusOK, a.ContentType, a.Bytes)
}

func isHexOrDash(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || r == '-'
}

// generatedAvatar draws a symmetric 5x5 pattern in one hue from the seed.
func generatedAvatar(seed string) []byte {
	sum := sha256.Sum256([]byte(seed))
	hue := int(sum[0])<<8 | int(sum[1])
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 5 5" width="96" height="96" shape-rendering="crispEdges">`)
	fmt.Fprintf(&b, `<rect width="5" height="5" fill="hsl(%d,45%%,92%%)"/>`, hue%360)
	for y := range 5 {
		for x := range 3 {
			if sum[2+y*3+x]&1 == 1 {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="hsl(%d,55%%,48%%)"/>`, x, y, hue%360)
				if x < 2 {
					fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="hsl(%d,55%%,48%%)"/>`, 4-x, y, hue%360)
				}
			}
		}
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func originOf(raw string) (string, error) { return pagekey.OriginOf(raw) }
