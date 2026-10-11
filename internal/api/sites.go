package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/pagekey"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/verify"
)

func (s *Server) siteJSON(x model.Site, role string) gin.H {
	origins := make([]gin.H, 0, len(x.Origins))
	for _, o := range x.Origins {
		origins = append(origins, gin.H{"origin": o.Origin, "primary": o.Primary, "verified_at": timeOrNil(o.VerifiedAt),
			"last_checked_at": timeOrNil(o.LastCheckedAt), "failing_since": timeOrNil(o.FailingSince)})
	}
	return gin.H{
		"id": x.PublicID, "name": x.Name, "status": x.Status, "status_note": x.StatusNote, "role": role,
		"guest_policy": x.GuestPolicy, "member_policy": x.MemberPolicy, "sort_order": x.SortOrder,
		"close_after_days": x.CloseAfterDays, "query_keys": x.QueryKeys, "allow_localhost": x.AllowLocalhost,
		"owner_notify": x.OwnerNotify, "blocked_words": x.BlockedWords, "origins": origins,
		"created_at": x.CreatedAt.UTC(), "verified_at": timeOrNil(x.VerifiedAt), "approved_at": timeOrNil(x.ApprovedAt),
		"deleted_at": timeOrNil(x.DeletedAt),
		"embed": gin.H{"snippet": `<kite-comments site="` + x.PublicID + `"></kite-comments>` + "\n" +
			`<script src="` + s.origin + `/embed.js" crossorigin="anonymous" async></script>`},
	}
}

// siteMember loads the :site of a console route for one of its managers.
func (s *Server) siteMember(c *gin.Context) {
	site, err := s.Store.Site(c.Request.Context(), c.Param("site"))
	if err != nil {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	role, err := s.Store.Role(c.Request.Context(), site.ID, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if role == "" {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	c.Set("site", site)
	c.Set("role", role)
	c.Next()
}

func (s *Server) ownerOnly(c *gin.Context) {
	if c.GetString("role") != model.RoleOwner {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	c.Next()
}

func currentSite(c *gin.Context) model.Site {
	v, _ := c.Get("site")
	return v.(model.Site)
}

// siteOrigin normalizes an origin a site may use: https, or http on a
// development server.
func (s *Server) siteOrigin(raw string) (string, bool) {
	o, err := pagekey.NormalizeOrigin(raw)
	if err != nil || (!strings.HasPrefix(o, "https://") && !s.DevHTTPOrigins) {
		return "", false
	}
	return o, true
}

// listSites is GET /api/v1/sites.
func (s *Server) listSites(c *gin.Context) {
	list, err := s.Store.SitesOf(c.Request.Context(), currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, x := range list {
		j := s.siteJSON(x.Site, x.Role)
		j["pending_count"], j["reported_count"] = x.PendingCount, x.ReportedCount
		data = append(data, j)
	}
	private(c, http.StatusOK, gin.H{"data": data})
}

// createSite is POST /api/v1/sites. The guest policy must be chosen
// (sites.md section 4).
func (s *Server) createSite(c *gin.Context) {
	var in struct {
		Name        string   `json:"name"`
		Origin      string   `json:"origin"`
		Aliases     []string `json:"aliases"`
		GuestPolicy string   `json:"guest_policy"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	primary, ok := s.siteOrigin(in.Origin)
	if !ok || name == "" || utf8.RuneCountInString(name) > policy.SiteNameRunes || len(in.Aliases) > policy.AliasOrigins ||
		!slices.Contains([]string{model.GuestOff, model.GuestReview, model.GuestPublish}, in.GuestPolicy) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	origins := []string{primary}
	for _, a := range in.Aliases {
		o, ok := s.siteOrigin(a)
		if !ok {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		if !slices.Contains(origins, o) {
			origins = append(origins, o)
		}
	}
	u := currentUser(c)
	site, err := s.Store.CreateSite(c.Request.Context(), store.NewSite{Name: name, Origins: origins, CreatedBy: &u.ID})
	if err != nil {
		s.storeError(c, err)
		return
	}
	if in.GuestPolicy != model.GuestOff {
		if site, err = s.Store.UpdateSite(c.Request.Context(), site.ID, store.SiteSettings{GuestPolicy: &in.GuestPolicy}); err != nil {
			s.storeError(c, err)
			return
		}
	}
	private(c, http.StatusCreated, s.siteJSON(site, model.RoleOwner))
}

// getSite is GET /api/v1/sites/{site}.
func (s *Server) getSite(c *gin.Context) {
	private(c, http.StatusOK, s.siteJSON(currentSite(c), c.GetString("role")))
}

var queryKeyRE = regexp.MustCompile(`^[A-Za-z0-9_.\-\[\]]{1,50}$`)

// updateSite is PATCH /api/v1/sites/{site}.
func (s *Server) updateSite(c *gin.Context) {
	var in struct {
		Name           *string   `json:"name"`
		GuestPolicy    *string   `json:"guest_policy"`
		MemberPolicy   *string   `json:"member_policy"`
		SortOrder      *string   `json:"sort_order"`
		CloseAfterDays *int      `json:"close_after_days"`
		ClearClose     bool      `json:"clear_close_after_days"`
		QueryKeys      *[]string `json:"query_keys"`
		AllowLocalhost *bool     `json:"allow_localhost"`
		OwnerNotify    *string   `json:"owner_notify"`
		BlockedWords   *[]string `json:"blocked_words"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	p := store.SiteSettings{Name: in.Name, GuestPolicy: in.GuestPolicy, MemberPolicy: in.MemberPolicy, SortOrder: in.SortOrder,
		AllowLocalhost: in.AllowLocalhost, OwnerNotify: in.OwnerNotify}
	bad := false
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		bad = bad || n == "" || utf8.RuneCountInString(n) > policy.SiteNameRunes
		p.Name = &n
	}
	if in.GuestPolicy != nil {
		bad = bad || !slices.Contains([]string{model.GuestOff, model.GuestReview, model.GuestPublish}, *in.GuestPolicy)
	}
	if in.MemberPolicy != nil {
		bad = bad || !slices.Contains([]string{model.MemberPublish, model.MemberReview}, *in.MemberPolicy)
	}
	if in.SortOrder != nil {
		bad = bad || !slices.Contains([]string{model.SortOldest, model.SortNewest}, *in.SortOrder)
	}
	if in.OwnerNotify != nil {
		bad = bad || !slices.Contains([]string{model.NotifyInstant, model.NotifyDaily, model.NotifyOff}, *in.OwnerNotify)
	}
	if in.ClearClose {
		var none *int
		p.CloseAfterDays = &none
	} else if in.CloseAfterDays != nil {
		bad = bad || *in.CloseAfterDays < 1 || *in.CloseAfterDays > 3650
		p.CloseAfterDays = &in.CloseAfterDays
	}
	if in.QueryKeys != nil {
		keys := slices.Compact(slices.Sorted(slices.Values(*in.QueryKeys)))
		bad = bad || len(keys) > policy.QueryKeys
		for _, k := range keys {
			bad = bad || !queryKeyRE.MatchString(k)
		}
		p.QueryKeys = &keys
	}
	if in.BlockedWords != nil {
		words := spam.NormalizeWords(*in.BlockedWords)
		bad = bad || len(words) > policy.BlockedWords
		for _, w := range words {
			bad = bad || utf8.RuneCountInString(w) > policy.BlockedWordRunes
		}
		p.BlockedWords = &words
	}
	if bad {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	site, err := s.Store.UpdateSite(c.Request.Context(), currentSite(c).ID, p)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, s.siteJSON(site, c.GetString("role")))
}

// deleteSite is DELETE /api/v1/sites/{site}: the recovery window starts.
func (s *Server) deleteSite(c *gin.Context) {
	if err := s.Store.DeleteSite(c.Request.Context(), currentSite(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// restoreSite is POST /api/v1/sites/{site}/restore.
func (s *Server) restoreSite(c *gin.Context) {
	if err := s.Store.RestoreSite(c.Request.Context(), currentSite(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	s.reloadSite(c)
}

func (s *Server) reloadSite(c *gin.Context) {
	site, err := s.Store.SiteByID(c.Request.Context(), currentSite(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, s.siteJSON(site, c.GetString("role")))
}

// addOrigin is POST /api/v1/sites/{site}/origins.
func (s *Server) addOrigin(c *gin.Context) {
	var in struct {
		Origin string `json:"origin"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	o, ok := s.siteOrigin(in.Origin)
	site := currentSite(c)
	if !ok || len(site.Origins) >= 1+policy.AliasOrigins {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if err := s.Store.AddOrigin(c.Request.Context(), site.ID, o); err != nil {
		s.storeError(c, err)
		return
	}
	s.reloadSite(c)
}

// removeOrigin is DELETE /api/v1/sites/{site}/origins/{origin}, with the
// origin percent-encoded.
func (s *Server) removeOrigin(c *gin.Context) {
	o, err := pagekey.NormalizeOrigin(c.Param("origin"))
	if err != nil {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if err := s.Store.RemoveOrigin(c.Request.Context(), currentSite(c).ID, o); err != nil {
		s.storeError(c, err)
		return
	}
	s.reloadSite(c)
}

// newVerification is POST /api/v1/sites/{site}/verification. The token is
// shown once, here.
func (s *Server) newVerification(c *gin.Context) {
	token := crypt.Token()
	expires := s.Now().Add(policy.VerificationTokenTTL)
	if err := s.Store.CreateVerification(c.Request.Context(), currentSite(c).ID, currentUser(c).ID, crypt.Hash(token), expires); err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusCreated, gin.H{
		"token": token, "expires_at": expires.UTC(),
		"dns":  gin.H{"name": verify.TXTPrefix + "<host>", "value": verify.TXTValue + token},
		"meta": `<meta name="` + verify.MetaName + `" content="` + token + `">`,
		"file": gin.H{"path": verify.FilePath, "content": token},
	})
}

// checkVerification is POST /api/v1/sites/{site}/verification/check.
func (s *Server) checkVerification(c *gin.Context) {
	site := currentSite(c)
	if ok, retry := s.verifies.allow(site.PublicID); !ok {
		s.tooMany(c, retry)
		return
	}
	hashes, err := s.Store.VerificationHashes(c.Request.Context(), site.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	var proof []byte
	matches := func(tok string) bool {
		h := crypt.Hash(tok)
		for _, x := range hashes {
			if bytes.Equal(x, h) {
				proof = h
				return true
			}
		}
		return false
	}
	results := []verify.Result{}
	passed := 0
	for _, o := range site.Origins {
		if o.VerifiedAt != nil {
			continue
		}
		var r verify.Result
		if len(hashes) == 0 {
			r = verify.Result{Origin: o.Origin, Detail: "no live verification token; create one first"}
		} else {
			r = s.Fetcher.Check(c.Request.Context(), o.Origin, matches)
		}
		if r.OK {
			passed++
			if err := s.Store.MarkOriginVerified(c.Request.Context(), site.ID, o.Origin, proof); err != nil {
				s.storeError(c, err)
				return
			}
		} else if err := s.Store.RecordVerificationError(c.Request.Context(), site.ID, o.Origin+": "+r.Detail); err != nil {
			s.storeError(c, err)
			return
		}
		results = append(results, r)
	}
	updated, err := s.Store.SiteByID(c.Request.Context(), site.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	body := gin.H{"results": results, "site": s.siteJSON(updated, c.GetString("role"))}
	if len(results) > 0 && passed == 0 {
		body["error"] = gin.H{"code": codeVerificationFailed, "message": message(codeVerificationFailed, zh(c))}
		private(c, http.StatusUnprocessableEntity, body)
		return
	}
	private(c, http.StatusOK, body)
}

// queueJSON is a comment in the console: everything the widget shows a
// manager, plus where it is and a guest's address.
func (s *Server) queueJSON(site model.Site, cm model.Comment) gin.H {
	j := s.commentJSON(site, cm, true)
	j["status"] = cm.Status
	j["thread"] = gin.H{"id": strconv.FormatInt(cm.ThreadID, 10), "url": cm.ThreadURL, "title": cm.ThreadTitle}
	j["url"] = permalink(cm.ThreadURL, cm.ID)
	if cm.AuthorKind == model.AuthorGuest && cm.AuthorEmailEnc != nil {
		if e, err := s.Keys.Decrypt(cm.AuthorEmailEnc); err == nil {
			j["guest_email"] = e
		}
	}
	if _, ok := j["author"]; !ok {
		j["author"] = s.authorJSON(site, cm)
	}
	return j
}

// queue is GET /api/v1/sites/{site}/comments.
func (s *Server) queue(c *gin.Context) {
	site := currentSite(c)
	status := c.Query("status")
	if status != "" && !slices.Contains([]string{"pending", "published", "spam", "hidden", "deleted", "reported"}, status) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	var threadID int64
	if v := c.Query("thread"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		threadID = id
	}
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, next, err := s.Store.Queue(c.Request.Context(), store.QueueQuery{SiteID: site.ID, Status: status, ThreadID: threadID, Cursor: cur, Limit: limit})
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, cm := range list {
		data = append(data, s.queueJSON(site, cm))
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": encodeCursor(next)})
}

// bulkModerate is POST /api/v1/sites/{site}/comments/moderation.
func (s *Server) bulkModerate(c *gin.Context) {
	var in struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	if !slices.Contains([]string{"approve", "hide", "spam", "restore", "pin", "unpin"}, in.Action) || len(in.IDs) == 0 || len(in.IDs) > 200 {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	ids := make([]int64, 0, len(in.IDs))
	for _, v := range in.IDs {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		ids = append(ids, id)
	}
	u := currentUser(c)
	n, err := s.Store.ModerateMany(c.Request.Context(), currentSite(c).ID, ids, in.Action, &u.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, gin.H{"changed": n})
}

func threadJSON(t model.Thread) gin.H {
	return gin.H{"id": strconv.FormatInt(t.ID, 10), "key": t.PageKey, "url": t.URL, "title": t.Title,
		"published_at": timeOrNil(t.PagePublishedAt), "locked": t.LockedAt != nil, "comment_count": t.CommentCount,
		"last_comment_at": timeOrNil(t.LastCommentAt), "created_at": t.CreatedAt.UTC()}
}

// listThreads is GET /api/v1/sites/{site}/threads.
func (s *Server) listThreads(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, next, err := s.Store.ThreadsOf(c.Request.Context(), currentSite(c).ID, cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, t := range list {
		data = append(data, threadJSON(t))
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": encodeCursor(next)})
}

// updateThread is PATCH /api/v1/sites/{site}/threads/{thread}.
func (s *Server) updateThread(c *gin.Context) {
	id, ok := s.idParam(c, "thread")
	if !ok {
		return
	}
	var in struct {
		Title          *string `json:"title"`
		URL            *string `json:"url"`
		PublishedAt    *string `json:"published_at"`
		ClearPublished bool    `json:"clear_published_at"`
		Locked         *bool   `json:"locked"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	site := currentSite(c)
	u := store.ThreadUpdate{Locked: in.Locked}
	if in.Title != nil {
		title := truncate(*in.Title, policy.ThreadTitleRunes)
		u.Title = &title
	}
	if in.URL != nil {
		if origin, err := originOf(*in.URL); err != nil || !site.HasVerifiedOrigin(origin) {
			s.fail(c, http.StatusBadRequest, codeOriginNotAllowed)
			return
		}
		u.URL = in.URL
	}
	if in.ClearPublished {
		var none *time.Time
		u.PagePublished = &none
	} else if in.PublishedAt != nil {
		p := parsePublished(*in.PublishedAt)
		if p == nil {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return
		}
		u.PagePublished = &p
	}
	t, err := s.Store.UpdateThread(c.Request.Context(), site.ID, id, u, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, threadJSON(t))
}

// moveThread is POST /api/v1/sites/{site}/threads/{thread}/move.
func (s *Server) moveThread(c *gin.Context) {
	id, ok := s.idParam(c, "thread")
	if !ok {
		return
	}
	var in struct {
		ToKey string `json:"to_key"`
		ToURL string `json:"to_url"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	site := currentSite(c)
	key := in.ToKey
	if key == "" && in.ToURL != "" {
		k, origin, err := pagekey.FromURL(in.ToURL, site.QueryKeys)
		if err != nil || !site.HasVerifiedOrigin(origin) {
			s.fail(c, http.StatusBadRequest, codeOriginNotAllowed)
			return
		}
		key = k
	}
	if !pagekey.Valid(key) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	t, err := s.Store.MoveThread(c.Request.Context(), site.ID, id, key, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, threadJSON(t))
}

func banJSON(b store.Ban) gin.H {
	kind := "user"
	switch {
	case b.EmailHMAC != nil:
		kind = "email"
	case b.IPHMAC != nil:
		kind = "ip"
	}
	return gin.H{"id": strconv.FormatInt(b.ID, 10), "kind": kind, "author_id": b.AuthorID, "name": b.Name,
		"reason": b.Reason, "created_at": b.CreatedAt.UTC(), "expires_at": timeOrNil(b.ExpiresAt)}
}

// listBans is GET /api/v1/sites/{site}/bans.
func (s *Server) listBans(c *gin.Context) {
	list, err := s.Store.Bans(c.Request.Context(), currentSite(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, b := range list {
		data = append(data, banJSON(b))
	}
	private(c, http.StatusOK, gin.H{"data": data})
}

// banInput reads a ban: by a reader (author_id, or the author of
// comment_id), by a guest's email, or by the IP hash a comment was stored
// with (moderation.md section 4.1).
type banInput struct {
	Kind      string `json:"kind"`
	AuthorID  string `json:"author_id"`
	CommentID string `json:"comment_id"`
	Email     string `json:"email"`
	Reason    string `json:"reason"`
	Days      int    `json:"days"`
}

// banBase checks the reason and term every ban has.
func (s *Server) banBase(in banInput) (store.Ban, bool) {
	b := store.Ban{Reason: strings.TrimSpace(in.Reason)}
	if utf8.RuneCountInString(b.Reason) > 500 || in.Days < 0 || in.Days > 3650 {
		return b, false
	}
	if in.Days > 0 {
		e := s.Now().Add(time.Duration(in.Days) * 24 * time.Hour)
		b.ExpiresAt = &e
	}
	return b, true
}

func (s *Server) buildBan(c *gin.Context, siteID int64, in banInput) (store.Ban, bool) {
	ctx := c.Request.Context()
	b, ok := s.banBase(in)
	if !ok {
		return b, false
	}
	var cm *model.Comment
	if in.CommentID != "" {
		id, err := strconv.ParseInt(in.CommentID, 10, 64)
		if err != nil {
			return b, false
		}
		x, err := s.Store.Comment(ctx, id)
		if err != nil || (siteID != 0 && x.SiteID != siteID) {
			return b, false
		}
		cm = &x
	}
	switch in.Kind {
	case "user":
		switch {
		case cm != nil && cm.AuthorUserID != nil:
			b.UserID = cm.AuthorUserID
		case in.AuthorID != "" && siteID != 0:
			id, err := s.Store.UserByAuthorID(ctx, siteID, in.AuthorID)
			if err != nil {
				return b, false
			}
			b.UserID = &id
		default:
			return b, false
		}
	case "email":
		switch {
		case cm != nil && cm.AuthorEmailHMAC != nil:
			b.EmailHMAC = cm.AuthorEmailHMAC
		case validEmail(in.Email) != "":
			b.EmailHMAC = s.Keys.EmailHMAC(in.Email)
		default:
			return b, false
		}
	case "ip":
		if cm == nil || cm.ID == 0 {
			return b, false
		}
		ip, err := s.Store.CommentIPHash(ctx, cm.ID)
		if err != nil || ip == nil {
			return b, false
		}
		b.IPHMAC = ip
	default:
		return b, false
	}
	return b, true
}

// createBan is POST /api/v1/sites/{site}/bans.
func (s *Server) createBan(c *gin.Context) {
	var in banInput
	if !s.bindJSON(c, &in) {
		return
	}
	site := currentSite(c)
	b, ok := s.buildBan(c, site.ID, in)
	if !ok {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	b.SiteID = &site.ID
	id, err := s.Store.CreateBan(c.Request.Context(), b, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10)})
}

// deleteBan is DELETE /api/v1/sites/{site}/bans/{id}.
func (s *Server) deleteBan(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	if err := s.Store.DeleteBan(c.Request.Context(), currentSite(c).ID, id, currentUser(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) reportsJSON(list []store.ReportInfo, site func(store.ReportInfo) model.Site) []gin.H {
	data := make([]gin.H, 0, len(list))
	for _, r := range list {
		data = append(data, gin.H{"id": strconv.FormatInt(r.ID, 10), "reason": r.Reason, "note": r.Note, "status": r.Status,
			"created_at": r.CreatedAt.UTC(), "resolved_at": timeOrNil(r.ResolvedAt),
			"site": gin.H{"id": r.SiteID, "name": r.SiteName}, "comment": s.queueJSON(site(r), r.Comment)})
	}
	return data
}

// listReports is GET /api/v1/sites/{site}/reports.
func (s *Server) listReports(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	site := currentSite(c)
	list, err := s.Store.Reports(c.Request.Context(), site.ID, c.Query("status"), cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	var next any
	if len(list) == limit {
		last := list[len(list)-1]
		next = encodeCursor(&model.Cursor{At: last.CreatedAt, ID: last.ID})
	}
	private(c, http.StatusOK, gin.H{"data": s.reportsJSON(list, func(store.ReportInfo) model.Site { return site }), "next_cursor": next})
}

// resolveReport is PATCH /api/v1/sites/{site}/reports/{id}: resolve or
// dismiss, optionally hiding the comment or marking it spam.
func (s *Server) resolveReport(c *gin.Context) {
	s.resolveReportFor(c, currentSite(c).ID, "")
}

func (s *Server) resolveReportFor(c *gin.Context, siteID int64, eventPrefix string) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
		Action string `json:"action"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	if (in.Status != "resolved" && in.Status != "dismissed") || (in.Action != "" && in.Action != "hide" && in.Action != "spam") {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	reportSite, commentID, err := s.Store.ReportSite(ctx, id)
	if err != nil || (siteID != 0 && reportSite != siteID) {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	if err := s.Store.ResolveReport(ctx, siteID, id, in.Status, u.ID); err != nil {
		s.storeError(c, err)
		return
	}
	if in.Action != "" {
		ev := ""
		if eventPrefix != "" {
			ev = eventPrefix + in.Action
		}
		_, err := s.Store.Moderate(ctx, store.ModerateIn{SiteID: reportSite, CommentID: commentID, Action: in.Action, Actor: &u.ID,
			EventAction: ev, Detail: map[string]any{"report_id": id}})
		if err != nil && !errors.Is(err, store.ErrInvalid) {
			s.storeError(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

// listEvents is GET /api/v1/sites/{site}/events.
func (s *Server) listEvents(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, err := s.Store.Events(c.Request.Context(), currentSite(c).ID, cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, e := range list {
		data = append(data, gin.H{"id": strconv.FormatInt(e.ID, 10), "comment_id": idString(e.CommentID), "actor": e.ActorName,
			"action": e.Action, "detail": e.Detail, "created_at": e.CreatedAt.UTC()})
	}
	var next any
	if len(list) == limit {
		last := list[len(list)-1]
		next = encodeCursor(&model.Cursor{At: last.CreatedAt, ID: last.ID})
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": next})
}

// undoEvent is POST /api/v1/sites/{site}/events/{id}/undo, for thread
// moves.
func (s *Server) undoEvent(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	if err := s.Store.UndoMove(c.Request.Context(), currentSite(c).ID, id, currentUser(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// readUpload reads the "file" field of a multipart upload, bounded by the
// import limit.
func readUpload(c *gin.Context) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, policy.ImportMaxBytes+1<<20)
	fh, err := c.FormFile("file")
	if err != nil || fh.Size > policy.ImportMaxBytes {
		return nil, false
	}
	f, err := fh.Open()
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, policy.ImportMaxBytes+1))
	return b, err == nil && len(b) <= policy.ImportMaxBytes
}
