package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/markup"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/pagekey"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
)

// siteToken returns the site session token a request carries: the
// Authorization header for the localStorage carrier, else the site's
// partitioned cookie (login.md section 5).
func (s *Server) siteToken(c *gin.Context, site model.Site) (token string, fromCookie bool) {
	if v, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v), false
	}
	v, _ := c.Cookie(s.siteCookieName(site.PublicID))
	return v, true
}

// viewerOf resolves the reader behind a request on one site.
func (s *Server) viewerOf(c *gin.Context, site model.Site) (model.Viewer, bool) {
	token, fromCookie := s.siteToken(c, site)
	if len(token) != 43 {
		return model.Viewer{}, false
	}
	v, slid, err := s.Store.SiteViewer(c.Request.Context(), crypt.Hash(token), site.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, context.Canceled) {
			s.Log.Error("site session", "error", err)
		}
		return model.Viewer{}, false
	}
	if slid != nil && fromCookie {
		// The cookie slides with the session, or it would lapse first.
		s.setCookie(c, s.siteCookieName(site.PublicID), token, time.Until(*slid), http.SameSiteNoneMode, true)
	}
	return v, true
}

func (s *Server) viewerJSON(site model.Site, v model.Viewer) gin.H {
	avatar := generatedURL(v.AuthorID, v.DisplayName)
	if v.HasAvatar {
		avatar = "/avatars/" + v.AuthorID + "?v=" + url.QueryEscape(v.AvatarTag)
	}
	return gin.H{"author_id": v.AuthorID, "name": v.DisplayName, "avatar": avatar, "role": v.Role,
		"notify_replies": v.NotifyReplies, "has_email": v.HasEmail}
}

// activeSite loads a site that takes writes.
func (s *Server) activeSite(c *gin.Context, publicID string) (model.Site, bool) {
	site, err := s.Store.Site(c.Request.Context(), publicID)
	if err != nil || site.Status != model.SiteActive {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.storeError(c, err)
			return site, false
		}
		s.fail(c, http.StatusForbidden, codeSiteInactive)
		return site, false
	}
	return site, true
}

// redeem is POST /api/v1/widget/sessions: a finished login becomes a site
// session, set as the partitioned cookie and returned as a token.
func (s *Server) redeem(c *gin.Context) {
	var body struct {
		Site     string `json:"site"`
		Verifier string `json:"verifier"`
	}
	if !s.bindJSON(c, &body) {
		return
	}
	if raw, err := base64.RawURLEncoding.DecodeString(body.Verifier); err != nil || len(raw) != 32 {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	site, ok := s.activeSite(c, body.Site)
	if !ok {
		return
	}
	token := crypt.Token()
	expires := s.Now().Add(policy.SiteSessionTTL)
	v, err := s.Store.RedeemWidgetLogin(c.Request.Context(), site.ID, crypt.Hash(body.Verifier),
		store.SiteSessionIn{TokenHash: crypt.Hash(token), ClientLabel: clientLabel(c.GetHeader("User-Agent")), ExpiresAt: expires})
	switch {
	case errors.Is(err, store.ErrLoginPending):
		private(c, http.StatusAccepted, gin.H{"status": "pending"})
		return
	case errors.Is(err, store.ErrLoginGone):
		s.fail(c, http.StatusGone, codeLoginExpired)
		return
	case errors.Is(err, store.ErrInvalid):
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	case err != nil:
		s.storeError(c, err)
		return
	}
	s.setCookie(c, s.siteCookieName(site.PublicID), token, policy.SiteSessionTTL, http.SameSiteNoneMode, true)
	private(c, http.StatusCreated, gin.H{"token": token, "expires_at": expires.UTC(), "viewer": s.viewerJSON(site, v)})
}

// viewer is GET /api/v1/widget/viewer.
func (s *Server) viewer(c *gin.Context) {
	site, err := s.Store.Site(c.Request.Context(), c.Query("site"))
	if err != nil {
		private(c, http.StatusOK, gin.H{"viewer": nil})
		return
	}
	v, ok := s.viewerOf(c, site)
	if !ok || v.Suspended {
		private(c, http.StatusOK, gin.H{"viewer": nil})
		return
	}
	private(c, http.StatusOK, gin.H{"viewer": s.viewerJSON(site, v)})
}

// signOutSite is DELETE /api/v1/widget/session: sign out of this site.
func (s *Server) signOutSite(c *gin.Context) {
	site, err := s.Store.Site(c.Request.Context(), c.Query("site"))
	if err != nil {
		c.Status(http.StatusNoContent)
		return
	}
	if token, _ := s.siteToken(c, site); len(token) == 43 {
		if err := s.Store.DeleteSiteSession(c.Request.Context(), crypt.Hash(token), site.ID); err != nil {
			s.storeError(c, err)
			return
		}
	}
	s.setCookie(c, s.siteCookieName(site.PublicID), "", -1, http.SameSiteNoneMode, true)
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

// managerComments is GET /api/v1/widget/threads/comments, the manager's
// view of a page with every state.
func (s *Server) managerComments(c *gin.Context) {
	site, ok := s.readableSite(c, c.Query("site"))
	if !ok {
		return
	}
	v, ok := s.viewerOf(c, site)
	if !ok || !v.Manages() {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	key := c.Query("key")
	if !pagekey.Valid(key) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	statuses := []string{model.StatusPublished, model.StatusPending, model.StatusHidden, model.StatusSpam}
	if st := c.Query("status"); st != "" {
		if !contains(statuses, st) {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		statuses = []string{st}
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
	body, err := s.commentsBody(c, site, key, newest, cur, limit, around, statuses, true)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, body)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// commentIn is the body of POST /api/v1/comments.
type commentIn struct {
	Site      string `json:"site"`
	Key       string `json:"key"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Published string `json:"published"`
	ParentID  string `json:"parent_id"`
	Body      string `json:"body"`
	// Guest is only looked at to tell a guest comment that it is gone.
	Guest *struct{} `json:"guest"`
}

// validateBody applies the length and link limits (moderation.md 3.1).
func (s *Server) validateBody(c *gin.Context, body string) (markup.Result, bool) {
	body = strings.TrimSpace(body)
	n := utf8.RuneCountInString(body)
	if n == 0 {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return markup.Result{}, false
	}
	if n > policy.CommentMaxRunes {
		s.fail(c, http.StatusBadRequest, codeCommentTooLong)
		return markup.Result{}, false
	}
	r := markup.Render(body)
	if r.Links > policy.LinksPerComment {
		s.fail(c, http.StatusBadRequest, codeTooManyLinks)
		return markup.Result{}, false
	}
	return r, true
}

// threadClosed reports whether a page takes no new comments: locked, or
// past the site's automatic closing (sites.md section 4).
func (s *Server) threadClosed(site model.Site, t *model.Thread, published *time.Time) bool {
	if t != nil {
		if t.LockedAt != nil {
			return true
		}
		at := t.ClosesAt(site.CloseAfterDays)
		return at != nil && s.Now().After(*at)
	}
	if site.CloseAfterDays == nil || published == nil {
		return false
	}
	return s.Now().After(published.Add(time.Duration(*site.CloseAfterDays) * 24 * time.Hour))
}

func parsePublished(v string) *time.Time {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return &t
		}
	}
	return nil
}

// decision is the outcome of the publishing rules 5 to 7.
type decision struct {
	status string
	rule   string
}

// spamRules applies moderation.md section 2, rules 5 to 7, to a body.
func (s *Server) spamRules(c *gin.Context, site model.Site, r markup.Result, body string, user model.User) (decision, error) {
	ctx := c.Request.Context()
	if utf8.RuneCountInString(strings.TrimSpace(body)) >= crossSiteMinRunes {
		n, err := s.Store.SitesWithBody(ctx, site.ID, body)
		if err != nil {
			return decision{}, err
		}
		if n+1 >= policy.CrossSiteMaxSites {
			return decision{model.StatusSpam, "cross_site"}, nil
		}
	}
	listed, err := s.spamDomainSet(c)
	if err != nil {
		return decision{}, err
	}
	hosts := spam.Hosts(r.HTML)
	if u, err := url.Parse(user.HomepageURL); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	for _, h := range hosts {
		if spam.DomainListed(h, listed) {
			return decision{model.StatusSpam, "spam_domain"}, nil
		}
	}
	if w, ok := spam.BlockedWord(body, site.BlockedWords); ok {
		return decision{model.StatusPending, "blocked_word:" + w}, nil
	}
	if r.Links > 0 && spam.NewAccount(user.CreatedAt, s.Now()) {
		return decision{model.StatusPending, "new_account_link"}, nil
	}
	if site.MemberPolicy == model.MemberReview {
		return decision{model.StatusPending, ""}, nil
	}
	return decision{model.StatusPublished, ""}, nil
}

// crossSiteMinRunes leaves short bodies like "thanks" out of the
// cross-site rule (moderation.md section 3.1).
const crossSiteMinRunes = 10

func (s *Server) spamDomainSet(c *gin.Context) (map[string]bool, error) {
	s.spamMu.Lock()
	defer s.spamMu.Unlock()
	if s.spamDomains != nil && s.Now().Sub(s.spamLoaded) < time.Minute {
		return s.spamDomains, nil
	}
	list, err := s.Store.SpamDomains(c.Request.Context())
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, d := range list {
		m[spam.NormalizeDomain(d)] = true
	}
	s.spamDomains, s.spamLoaded = m, s.Now()
	return m, nil
}

// postComment is POST /api/v1/comments; the checks run in the order of
// moderation.md section 2.
func (s *Server) postComment(c *gin.Context) {
	ctx := c.Request.Context()
	var in commentIn
	if !s.bindJSON(c, &in) {
		return
	}
	site, ok := s.activeSite(c, in.Site)
	if !ok {
		return
	}
	page := s.resolvePage(site, in.URL, "")
	if page.Mode != modeNormal {
		s.fail(c, http.StatusBadRequest, codeOriginNotAllowed)
		return
	}
	key := page.Key
	if strings.HasPrefix(in.Key, "k:") {
		explicit, err := pagekey.Explicit(in.Key[2:])
		if err != nil || explicit != in.Key {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		key = explicit
	} else if in.Key != key {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	var parentID *int64
	if in.ParentID != "" {
		id, err := strconv.ParseInt(in.ParentID, 10, 64)
		if err != nil {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		parentID = &id
	}
	viewer, signedIn := s.viewerOf(c, site)
	if !signedIn && in.Guest != nil {
		s.guestGone(c)
		return
	}
	if !signedIn {
		s.fail(c, http.StatusUnauthorized, codeUnauthorized)
		return
	}

	// Rule 1: the page takes comments.
	var thread *model.Thread
	if t, err := s.Store.ThreadByKey(ctx, site.ID, key); err == nil {
		thread = &t
	} else if !errors.Is(err, store.ErrNotFound) {
		s.storeError(c, err)
		return
	}
	published := parsePublished(in.Published)
	if published != nil && published.After(s.Now().Add(policy.PublishedFutureTolerance)) {
		// A future date would let a commenter pre-close a page that is not
		// out yet (sites.md section 4).
		published = nil
	}
	if s.threadClosed(site, thread, published) {
		s.fail(c, http.StatusForbidden, codeThreadLocked)
		return
	}

	// Length and links first: a rejected body does not use up a limit.
	r, ok := s.validateBody(c, in.Body)
	if !ok {
		return
	}
	n := store.NewComment{SiteID: site.ID, ThreadKey: key, ThreadURL: truncate(in.URL, policy.ThreadURLRunes),
		ThreadTitle: truncate(in.Title, policy.ThreadTitleRunes), PagePublished: published, ParentID: parentID,
		IPHMAC: s.Keys.IPHMAC(c.ClientIP(), s.Now())}
	ipKeys := s.Keys.IPHMACs(c.ClientIP(), s.Now())
	// Rule 2: suspended or banned readers.
	if viewer.Suspended {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	banned, err := s.Store.Banned(ctx, site.ID, &viewer.UserID, nil, ipKeys)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if banned {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	user, err := s.Store.User(ctx, viewer.UserID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	n.AuthorKind, n.AuthorUserID = model.AuthorUser, &viewer.UserID
	// Rule 4: limits.
	userKey := strconv.FormatInt(viewer.UserID, 10)
	if thread == nil {
		if ok, retry := s.userThreads.allow(userKey); !ok {
			s.tooMany(c, retry)
			return
		}
	}
	if ok, retry := s.memberSite.allow(userKey + ":" + site.PublicID); !ok {
		s.tooMany(c, retry)
		return
	}
	if ok, retry := s.memberAll.allow(userKey); !ok {
		s.tooMany(c, retry)
		return
	}

	body := strings.TrimSpace(in.Body)
	dup, err := s.Store.RecentDuplicate(ctx, viewer.UserID, body)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if dup {
		s.fail(c, http.StatusConflict, codeConflict)
		return
	}
	d, err := s.spamRules(c, site, r, body, user)
	if err != nil {
		s.storeError(c, err)
		return
	}
	n.BodyMD, n.BodyHTML, n.RenderVersion, n.Status = body, r.HTML, markup.Version, d.status
	switch {
	case d.status == model.StatusSpam:
		n.Event = &store.Event{Action: "auto_spam", Detail: map[string]any{"rule": d.rule}}
	case d.status == model.StatusPending && d.rule != "":
		n.Event = &store.Event{Action: "auto_pending", Detail: map[string]any{"rule": d.rule}}
	}
	cm, err := s.Store.CreateComment(ctx, n)
	if err != nil {
		s.storeError(c, err)
		return
	}
	out := s.commentJSON(site, cm, true)
	// Spam looks like review to its author (moderation.md section 2, rule 5).
	if cm.Status == model.StatusSpam {
		out["status"] = model.StatusPending
	}
	private(c, http.StatusCreated, out)
}

// ownComment loads a comment of the signed-in reader on the site. Only an
// active site takes edits; deleting works on any site that still exists,
// since a comment is its author's (moderation.md section 1).
func (s *Server) ownComment(c *gin.Context, sitePublicID string, anyStatus bool) (model.Site, model.Viewer, model.Comment, bool) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return model.Site{}, model.Viewer{}, model.Comment{}, false
	}
	var site model.Site
	if anyStatus {
		x, err := s.Store.Site(c.Request.Context(), sitePublicID)
		if err != nil {
			s.fail(c, http.StatusNotFound, codeNotFound)
			return site, model.Viewer{}, model.Comment{}, false
		}
		site = x
	} else if site, ok = s.activeSite(c, sitePublicID); !ok {
		return site, model.Viewer{}, model.Comment{}, false
	}
	v, ok := s.viewerOf(c, site)
	if !ok {
		s.fail(c, http.StatusUnauthorized, codeUnauthorized)
		return site, v, model.Comment{}, false
	}
	cm, err := s.Store.Comment(c.Request.Context(), id)
	if err != nil || cm.SiteID != site.ID || cm.AuthorUserID == nil || *cm.AuthorUserID != v.UserID {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return site, v, cm, false
	}
	return site, v, cm, true
}

// editComment is PATCH /api/v1/comments/{id}.
func (s *Server) editComment(c *gin.Context) {
	var in struct {
		Site string `json:"site"`
		Body string `json:"body"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	site, v, cm, ok := s.ownComment(c, in.Site, false)
	if !ok {
		return
	}
	if v.Suspended {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	// A banned reader cannot change what they wrote either.
	banned, err := s.Store.Banned(c.Request.Context(), site.ID, &v.UserID, nil, s.Keys.IPHMACs(c.ClientIP(), s.Now()))
	if err != nil {
		s.storeError(c, err)
		return
	}
	if banned {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	if s.Now().Sub(cm.CreatedAt) > policy.EditWindow || (cm.Status != model.StatusPublished && cm.Status != model.StatusPending) {
		s.fail(c, http.StatusForbidden, codeEditWindowClosed)
		return
	}
	r, ok := s.validateBody(c, in.Body)
	if !ok {
		return
	}
	u, err := s.Store.User(c.Request.Context(), v.UserID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	body := strings.TrimSpace(in.Body)
	d, err := s.spamRules(c, site, r, body, u)
	if err != nil {
		s.storeError(c, err)
		return
	}
	next := ""
	if d.status != model.StatusPublished {
		next = d.status
	}
	updated, err := s.Store.EditComment(c.Request.Context(), store.EditIn{CommentID: cm.ID, UserID: v.UserID, BodyMD: body,
		BodyHTML: r.HTML, RenderVersion: markup.Version, Status: next, Rule: d.rule})
	if err != nil {
		s.storeError(c, err)
		return
	}
	out := s.commentJSON(site, updated, true)
	if updated.Status == model.StatusSpam {
		out["status"] = model.StatusPending
	}
	private(c, http.StatusOK, out)
}

// deleteComment is DELETE /api/v1/comments/{id}.
func (s *Server) deleteComment(c *gin.Context) {
	_, v, cm, ok := s.ownComment(c, c.Query("site"), true)
	if !ok {
		return
	}
	if err := s.Store.DeleteOwnComment(c.Request.Context(), cm.ID, v.UserID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

// reportComment is POST /api/v1/comments/{id}/reports.
func (s *Server) reportComment(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Site   string `json:"site"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	site, ok := s.readableSite(c, in.Site)
	if !ok {
		return
	}
	v, ok := s.viewerOf(c, site)
	if !ok {
		s.fail(c, http.StatusUnauthorized, codeUnauthorized)
		return
	}
	if !contains([]string{"spam", "abuse", "illegal", "privacy", "other"}, in.Reason) ||
		utf8.RuneCountInString(in.Note) > policy.ReportNoteRunes {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	cm, err := s.Store.Comment(ctx, id)
	if err != nil || cm.SiteID != site.ID || cm.Status != model.StatusPublished {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if ok, retry := s.reports.allow(strconv.FormatInt(v.UserID, 10)); !ok {
		s.tooMany(c, retry)
		return
	}
	trusted, err := s.Store.Trusted(ctx, v.UserID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	res, err := s.Store.CreateReport(ctx, store.ReportIn{SiteID: site.ID, CommentID: id, ReporterID: v.UserID,
		Reason: in.Reason, Note: strings.TrimSpace(in.Note), ReporterTrusted: trusted})
	if err != nil {
		s.storeError(c, err)
		return
	}
	if !res.Duplicate && (in.Reason == "illegal" || in.Reason == "privacy") {
		s.notifyUrgentReport(c, site, res.ID)
	}
	private(c, http.StatusCreated, gin.H{"id": strconv.FormatInt(res.ID, 10)})
}

// moderateComment is POST /api/v1/comments/{id}/moderation, the manager's
// actions in the widget.
func (s *Server) moderateComment(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Site   string `json:"site"`
		Action string `json:"action"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	if !contains([]string{"approve", "hide", "spam", "restore", "pin", "unpin"}, in.Action) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	site, ok := s.readableSite(c, in.Site)
	if !ok {
		return
	}
	v, ok := s.viewerOf(c, site)
	if !ok || !v.Manages() {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	cm, err := s.Store.Moderate(c.Request.Context(), store.ModerateIn{SiteID: site.ID, CommentID: id, Action: in.Action, Actor: &v.UserID})
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, s.commentJSON(site, cm, true))
}

// lockThread is PUT /api/v1/threads/lock.
func (s *Server) lockThread(c *gin.Context) {
	var in struct {
		Site   string `json:"site"`
		Key    string `json:"key"`
		Locked bool   `json:"locked"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	site, ok := s.readableSite(c, in.Site)
	if !ok {
		return
	}
	v, ok := s.viewerOf(c, site)
	if !ok || !v.Manages() {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	t, err := s.Store.ThreadByKey(c.Request.Context(), site.ID, in.Key)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if err := s.Store.LockThread(c.Request.Context(), site.ID, t.ID, in.Locked, &v.UserID); err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, gin.H{"locked": in.Locked})
}

// preview is POST /api/v1/preview, rendered as a comment would be.
func (s *Server) preview(c *gin.Context) {
	var in struct {
		Body string `json:"body"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	if utf8.RuneCountInString(in.Body) > policy.CommentMaxRunes {
		s.fail(c, http.StatusBadRequest, codeCommentTooLong)
		return
	}
	private(c, http.StatusOK, gin.H{"html": markup.Render(in.Body).HTML})
}

func validEmail(raw string) string {
	email := crypt.NormalizeEmail(raw)
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 || !strings.Contains(email, ".") {
		return ""
	}
	return email
}

// validHomepage accepts an http or https address of up to 300 characters.
func validHomepage(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || utf8.RuneCountInString(raw) > policy.HomepageMaxRunes {
		return "", false
	}
	return u.String(), true
}

// commentSource is GET /api/v1/comments/{id}/source: the author's own
// Markdown, for editing in the widget.
func (s *Server) commentSource(c *gin.Context) {
	_, _, cm, ok := s.ownComment(c, c.Query("site"), false)
	if !ok {
		return
	}
	private(c, http.StatusOK, gin.H{"body": cm.BodyMD})
}
