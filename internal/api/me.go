package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
)

func (s *Server) meJSON(c *gin.Context, u model.User) (gin.H, error) {
	email := ""
	if u.EmailEnc != nil {
		if e, err := s.Keys.Decrypt(u.EmailEnc); err == nil {
			email = e
		}
	}
	sites, err := s.Store.SitesOf(c.Request.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	mine := make([]gin.H, 0, len(sites))
	for _, x := range sites {
		mine = append(mine, gin.H{"id": x.PublicID, "name": x.Name, "role": x.Role, "status": x.Status})
	}
	avatar := "/avatars/generated/" + shortHash("me:"+u.PublicID) + ".svg"
	if u.AvatarKind == "idp" && u.HasAvatar {
		avatar = "/api/v1/me/avatar"
	}
	return gin.H{
		"id": u.PublicID, "display_name": u.DisplayName, "avatar": avatar, "avatar_kind": u.AvatarKind,
		"has_idp_avatar": u.HasAvatar, "homepage_url": u.HomepageURL, "locale": u.Locale, "email": email,
		"notify_replies": u.NotifyReplies, "on_account_delete": u.OnAccountDelete, "is_platform_admin": u.IsPlatformAdmin,
		"suspended": u.SuspendedAt != nil, "created_at": u.CreatedAt.UTC(), "sites": mine,
		"csrf_token": csrfToken(c.GetString("session_token")),
	}, nil
}

// me is GET /api/v1/me.
func (s *Server) me(c *gin.Context) {
	body, err := s.meJSON(c, currentUser(c))
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, body)
}

// updateMe is PATCH /api/v1/me.
func (s *Server) updateMe(c *gin.Context) {
	var in struct {
		DisplayName     *string `json:"display_name"`
		AvatarKind      *string `json:"avatar_kind"`
		HomepageURL     *string `json:"homepage_url"`
		Locale          *string `json:"locale"`
		NotifyReplies   *bool   `json:"notify_replies"`
		OnAccountDelete *string `json:"on_account_delete"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	p := store.ProfileUpdate{NotifyReplies: in.NotifyReplies}
	bad := false
	if in.DisplayName != nil {
		name := spam.CleanName(*in.DisplayName)
		bad = bad || name == "" || utf8.RuneCountInString(name) > policy.DisplayNameRunes
		p.DisplayName = &name
	}
	if in.AvatarKind != nil {
		bad = bad || (*in.AvatarKind != "generated" && *in.AvatarKind != "idp")
		p.AvatarKind = in.AvatarKind
	}
	if in.HomepageURL != nil {
		h, ok := validHomepage(*in.HomepageURL)
		bad = bad || !ok
		p.HomepageURL = &h
	}
	if in.Locale != nil {
		bad = bad || (*in.Locale != model.LocaleZH && *in.Locale != model.LocaleEN)
		p.Locale = in.Locale
	}
	if in.OnAccountDelete != nil {
		bad = bad || (*in.OnAccountDelete != "remove" && *in.OnAccountDelete != "anonymize")
		p.OnAccountDelete = in.OnAccountDelete
	}
	if bad {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	u, err := s.Store.UpdateProfile(c.Request.Context(), currentUser(c).ID, p)
	if err != nil {
		s.storeError(c, err)
		return
	}
	body, err := s.meJSON(c, u)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, body)
}

// deleteMe is DELETE /api/v1/me: this service's data goes; the Kite Plus
// account stays (login.md section 7).
func (s *Server) deleteMe(c *gin.Context) {
	var in struct {
		Comments string `json:"comments"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	if in.Comments != "remove" && in.Comments != "anonymize" {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if err := s.Store.DeleteUserData(c.Request.Context(), currentUser(c).ID, in.Comments, false); err != nil {
		s.storeError(c, err)
		return
	}
	s.setCookie(c, s.cookieName("comments_session"), "", -1, http.SameSiteLaxMode, false)
	c.Status(http.StatusNoContent)
}

// myAvatar is GET /api/v1/me/avatar.
func (s *Server) myAvatar(c *gin.Context) {
	a, err := s.Store.AvatarByUser(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, a.ContentType, a.Bytes)
}

// placedJSON describes one of the reader's comments with where it is.
func placedJSON(cm model.Comment) gin.H {
	return gin.H{
		"id": strconv.FormatInt(cm.ID, 10), "root_id": idString(cm.RootID), "parent_id": idString(cm.ParentID),
		"site":       gin.H{"id": cm.SitePublic, "name": cm.SiteName},
		"thread":     gin.H{"id": strconv.FormatInt(cm.ThreadID, 10), "url": cm.ThreadURL, "title": cm.ThreadTitle},
		"url":        permalink(cm.ThreadURL, cm.ID),
		"status":     cm.Status,
		"body_html":  cm.BodyHTML,
		"author":     gin.H{"kind": cm.AuthorKind, "name": cm.AuthorName},
		"created_at": cm.CreatedAt.UTC(), "edited_at": timeOrNil(cm.EditedAt),
	}
}

func permalink(threadURL string, id int64) string {
	base, _, _ := strings.Cut(threadURL, "#")
	return base + "#kc-" + strconv.FormatInt(id, 10)
}

// myComments is GET /api/v1/me/comments.
func (s *Server) myComments(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, next, err := s.Store.MyComments(c.Request.Context(), currentUser(c).ID, c.Query("site"), cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, cm := range list {
		data = append(data, placedJSON(cm))
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": encodeCursor(next)})
}

// myNotifications is GET /api/v1/me/notifications.
func (s *Server) myNotifications(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, next, unread, err := s.Store.Notifications(c.Request.Context(), currentUser(c).ID, cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, n := range list {
		data = append(data, gin.H{"id": strconv.FormatInt(n.ID, 10), "kind": n.Kind, "created_at": n.CreatedAt.UTC(),
			"read_at": timeOrNil(n.ReadAt), "comment": placedJSON(n.Comment)})
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": encodeCursor(next), "unread": unread})
}

// markRead is POST /api/v1/me/notifications/read.
func (s *Server) markRead(c *gin.Context) {
	var in struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	var ids []int64
	if !in.All {
		if len(in.IDs) == 0 || len(in.IDs) > 500 {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		for _, v := range in.IDs {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				s.fail(c, http.StatusBadRequest, codeInvalidRequest)
				return
			}
			ids = append(ids, id)
		}
	}
	if err := s.Store.MarkRead(c.Request.Context(), currentUser(c).ID, ids); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// myMutes is GET /api/v1/me/mutes.
func (s *Server) myMutes(c *gin.Context) {
	ids, err := s.Store.Muted(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]string, 0, len(ids))
	for _, id := range ids {
		data = append(data, strconv.FormatInt(id, 10))
	}
	private(c, http.StatusOK, gin.H{"data": data})
}

func (s *Server) muteChange(c *gin.Context, mute bool) {
	id, ok := s.idParam(c, "thread")
	if !ok {
		return
	}
	exists, err := s.Store.ThreadExists(c.Request.Context(), id)
	if err != nil || !exists {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if mute {
		err = s.Store.Mute(c.Request.Context(), currentUser(c).ID, id)
	} else {
		err = s.Store.Unmute(c.Request.Context(), currentUser(c).ID, id)
	}
	if err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) mute(c *gin.Context)   { s.muteChange(c, true) }
func (s *Server) unmute(c *gin.Context) { s.muteChange(c, false) }

// mySiteSessions is GET /api/v1/me/site-sessions.
func (s *Server) mySiteSessions(c *gin.Context) {
	list, err := s.Store.SiteSessions(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, x := range list {
		data = append(data, gin.H{"id": x.ID, "site": gin.H{"id": x.SiteID, "name": x.SiteName, "origin": x.Origin},
			"client": x.ClientLabel, "created_via": x.CreatedVia, "created_at": x.CreatedAt.UTC(),
			"last_used_at": x.LastUsedAt.UTC(), "expires_at": x.ExpiresAt.UTC()})
	}
	private(c, http.StatusOK, gin.H{"data": data})
}

// endSiteSession is DELETE /api/v1/me/site-sessions/{id}.
func (s *Server) endSiteSession(c *gin.Context) {
	if err := s.Store.DeleteSiteSessionByID(c.Request.Context(), currentUser(c).ID, c.Param("id")); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// endAllSiteSessions is DELETE /api/v1/me/site-sessions, "sign out of all
// sites".
func (s *Server) endAllSiteSessions(c *gin.Context) {
	n, err := s.Store.DeleteAllSiteSessions(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, gin.H{"ended": n})
}

// exportMe is GET /api/v1/me/export: the reader's profile and every
// comment, as a file (sites.md section 8).
func (s *Server) exportMe(c *gin.Context) {
	u := currentUser(c)
	list, _, err := s.Store.MyComments(c.Request.Context(), u.ID, "", nil, 1_000_000)
	if err != nil {
		s.storeError(c, err)
		return
	}
	profile, err := s.meJSON(c, u)
	if err != nil {
		s.storeError(c, err)
		return
	}
	delete(profile, "csrf_token")
	comments := make([]gin.H, 0, len(list))
	for _, cm := range list {
		comments = append(comments, gin.H{
			"id": strconv.FormatInt(cm.ID, 10), "site": gin.H{"id": cm.SitePublic, "name": cm.SiteName},
			"thread": gin.H{"url": cm.ThreadURL, "title": cm.ThreadTitle}, "url": permalink(cm.ThreadURL, cm.ID),
			"parent_id": idString(cm.ParentID), "root_id": idString(cm.RootID),
			"body": gin.H{"format": "markdown", "source": cm.BodyMD}, "status": cm.Status,
			"created_at": cm.CreatedAt.UTC(), "edited_at": timeOrNil(cm.EditedAt),
		})
	}
	c.Header("Content-Disposition", `attachment; filename="kite-comments-me-`+time.Now().UTC().Format("20060102")+`.json"`)
	private(c, http.StatusOK, gin.H{"format": "kite-comments-reader", "version": 1, "exported_at": s.Now().UTC(),
		"profile": profile, "comments": comments})
}

// startClaim is POST /api/v1/me/claims (sites.md section 6). It answers
// 202 whether or not the address left any comment.
func (s *Server) startClaim(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	email := validEmail(in.Email)
	if email == "" {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	u := currentUser(c)
	hmac := s.Keys.EmailHMAC(email)
	// The account's own verified address needs no mail (step 6).
	own := u.EmailHMAC != nil && string(u.EmailHMAC) == string(hmac)
	if !own {
		if ok, retry := s.claims.allow(strconv.FormatInt(u.ID, 10)); !ok {
			s.tooMany(c, retry)
			return
		}
	}
	token := crypt.Token()
	id, err := s.Store.CreateClaim(c.Request.Context(), u.ID, hmac, crypt.Hash(token), s.Now().Add(policy.ClaimLinkTTL), own)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if !own {
		payload := encodeJSON(gin.H{"claim_id": id, "token": token, "user_id": u.ID})
		if err := s.Store.EnqueueMail(c.Request.Context(), store.Mail{Kind: "claim", ToEnc: s.Keys.Encrypt(email), Locale: u.Locale, Payload: payload}); err != nil {
			s.storeError(c, err)
			return
		}
	}
	private(c, http.StatusAccepted, gin.H{"id": strconv.FormatInt(id, 10), "verified": own})
}

func claimSummary(sum store.ClaimSummary) gin.H {
	sites := make([]gin.H, 0, len(sum.Sites))
	for _, x := range sum.Sites {
		sites = append(sites, gin.H{"id": x.PublicID, "name": x.Name, "count": x.Count})
	}
	return gin.H{"count": sum.Count, "sites": sites}
}

// verifyClaim is POST /api/v1/me/claims/{id}/verify. Only the account that
// started the claim can verify it; the token comes from the mail link.
func (s *Server) verifyClaim(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	sum, err := s.Store.VerifyClaim(c.Request.Context(), id, currentUser(c).ID, crypt.Hash(in.Token))
	if errors.Is(err, store.ErrNotFound) {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, claimSummary(sum))
}

// confirmClaim is POST /api/v1/me/claims/{id}/confirm.
func (s *Server) confirmClaim(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	n, err := s.Store.ConfirmClaim(c.Request.Context(), id, currentUser(c).ID)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, gin.H{"claimed": n})
}

// claimSuggestion is GET /api/v1/me/claims/suggestion: guest comments left
// with the account's own verified address (sites.md section 6, step 6).
func (s *Server) claimSuggestion(c *gin.Context) {
	u := currentUser(c)
	if u.EmailHMAC == nil {
		private(c, http.StatusOK, gin.H{"count": 0, "sites": []any{}})
		return
	}
	sum, err := s.Store.ClaimCounts(c.Request.Context(), u.EmailHMAC)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, claimSummary(sum))
}

func profileNotify(v *bool) store.ProfileUpdate { return store.ProfileUpdate{NotifyReplies: v} }
