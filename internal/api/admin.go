package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
)

// adminSites is GET /api/v1/admin/sites?status=.
func (s *Server) adminSites(c *gin.Context) {
	status := c.Query("status")
	if status != "" && !slices.Contains([]string{model.SiteUnverified, model.SitePendingReview, model.SiteActive, model.SiteSuspended, model.SiteDeleted}, status) {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, err := s.Store.AdminSites(c.Request.Context(), status, cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	data := make([]gin.H, 0, len(list))
	for _, x := range list {
		data = append(data, s.siteJSON(x, ""))
	}
	var next any
	if len(list) == limit {
		last := list[len(list)-1]
		next = encodeCursor(&model.Cursor{At: last.CreatedAt, ID: last.ID})
	}
	private(c, http.StatusOK, gin.H{"data": data, "next_cursor": next})
}

func (s *Server) adminSite(c *gin.Context) (model.Site, bool) {
	site, err := s.Store.Site(c.Request.Context(), c.Param("site"))
	if err != nil {
		s.storeError(c, err)
		return site, false
	}
	return site, true
}

func (s *Server) adminSiteAction(c *gin.Context, act func(model.Site, string) error, event string) {
	site, ok := s.adminSite(c)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if c.Request.ContentLength > 0 && !s.bindJSON(c, &in) {
		return
	}
	if err := act(site, strings.TrimSpace(in.Note)); err != nil {
		s.storeError(c, err)
		return
	}
	u := currentUser(c)
	if event != "" {
		_ = s.Store.AddEvent(c.Request.Context(), site.ID, nil, store.Event{Action: event, Actor: &u.ID, Detail: map[string]any{"note": in.Note}})
	}
	site, err := s.Store.SiteByID(c.Request.Context(), site.ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusOK, s.siteJSON(site, ""))
}

// adminApprove is POST /api/v1/admin/sites/{site}/approve.
func (s *Server) adminApprove(c *gin.Context) {
	s.adminSiteAction(c, func(x model.Site, _ string) error { return s.Store.ApproveSite(c.Request.Context(), x.ID) }, "")
}

// adminSuspend is POST /api/v1/admin/sites/{site}/suspend.
func (s *Server) adminSuspend(c *gin.Context) {
	s.adminSiteAction(c, func(x model.Site, note string) error { return s.Store.SuspendSite(c.Request.Context(), x.ID, note) },
		"platform_suspend_site")
}

// adminUnsuspend is POST /api/v1/admin/sites/{site}/unsuspend.
func (s *Server) adminUnsuspend(c *gin.Context) {
	s.adminSiteAction(c, func(x model.Site, _ string) error { return s.Store.UnsuspendSite(c.Request.Context(), x.ID) }, "")
}

// adminReports is GET /api/v1/admin/reports: the platform's queue of
// illegal and privacy reports (moderation.md section 4.2).
func (s *Server) adminReports(c *gin.Context) {
	cur, limit, ok := s.page(c, policy.ListPage, policy.ListPageMax)
	if !ok {
		return
	}
	list, err := s.Store.Reports(c.Request.Context(), 0, c.Query("status"), cur, limit)
	if err != nil {
		s.storeError(c, err)
		return
	}
	sites := map[int64]model.Site{}
	siteOf := func(r store.ReportInfo) model.Site {
		if x, ok := sites[r.Comment.SiteID]; ok {
			return x
		}
		x, _ := s.Store.SiteByID(c.Request.Context(), r.Comment.SiteID)
		sites[r.Comment.SiteID] = x
		return x
	}
	var next any
	if len(list) == limit {
		last := list[len(list)-1]
		next = encodeCursor(&model.Cursor{At: last.CreatedAt, ID: last.ID})
	}
	private(c, http.StatusOK, gin.H{"data": s.reportsJSON(list, siteOf), "next_cursor": next})
}

// adminResolveReport is PATCH /api/v1/admin/reports/{id}.
func (s *Server) adminResolveReport(c *gin.Context) { s.resolveReportFor(c, 0, "platform_") }

// adminHide is POST /api/v1/admin/comments/{id}/hide.
func (s *Server) adminHide(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	cm, err := s.Store.Comment(c.Request.Context(), id)
	if err != nil {
		s.storeError(c, err)
		return
	}
	u := currentUser(c)
	if _, err := s.Store.Moderate(c.Request.Context(), store.ModerateIn{SiteID: cm.SiteID, CommentID: id, Action: "hide",
		Actor: &u.ID, EventAction: "platform_hide"}); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) adminUser(c *gin.Context) (model.User, bool) {
	u, err := s.Store.UserByPublicID(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.storeError(c, err)
		return u, false
	}
	return u, true
}

// adminSuspendUser is POST /api/v1/admin/users/{id}/suspend; {id} is the
// account's public ID.
func (s *Server) adminSuspendUser(c *gin.Context) {
	u, ok := s.adminUser(c)
	if !ok {
		return
	}
	if err := s.Store.SuspendUser(c.Request.Context(), u.ID, "platform"); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminUnsuspendUser is POST /api/v1/admin/users/{id}/unsuspend.
func (s *Server) adminUnsuspendUser(c *gin.Context) {
	u, ok := s.adminUser(c)
	if !ok {
		return
	}
	if err := s.Store.UnsuspendUser(c.Request.Context(), u.ID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminBans is GET /api/v1/admin/bans: platform-wide bans.
func (s *Server) adminBans(c *gin.Context) {
	list, err := s.Store.Bans(c.Request.Context(), 0)
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

// adminCreateBan is POST /api/v1/admin/bans. A reader is named by a
// comment of theirs or by their account's public ID in user_id.
func (s *Server) adminCreateBan(c *gin.Context) {
	var in struct {
		banInput
		UserID string `json:"user_id"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	var b store.Ban
	ok := false
	if in.Kind == "user" && in.UserID != "" && in.CommentID == "" {
		if b, ok = s.banBase(in.banInput); ok {
			u, err := s.Store.UserByPublicID(c.Request.Context(), in.UserID)
			if ok = err == nil; ok {
				b, ok = s.banIdentity(c, b, u.ID)
			}
		}
	} else {
		b, ok = s.buildBan(c, 0, in.banInput)
	}
	if !ok {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	id, err := s.Store.CreateBan(c.Request.Context(), b, currentUser(c).ID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	private(c, http.StatusCreated, gin.H{"id": strconv.FormatInt(id, 10)})
}

// adminDeleteBan is DELETE /api/v1/admin/bans/{id}.
func (s *Server) adminDeleteBan(c *gin.Context) {
	id, ok := s.idParam(c, "id")
	if !ok {
		return
	}
	if err := s.Store.DeleteBan(c.Request.Context(), 0, id, currentUser(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminSpamDomains is GET /api/v1/admin/spam-domains.
func (s *Server) adminSpamDomains(c *gin.Context) {
	list, err := s.Store.SpamDomains(c.Request.Context())
	if err != nil {
		s.storeError(c, err)
		return
	}
	if list == nil {
		list = []string{}
	}
	private(c, http.StatusOK, gin.H{"data": list})
}

// adminAddSpamDomain is POST /api/v1/admin/spam-domains.
func (s *Server) adminAddSpamDomain(c *gin.Context) {
	var in struct {
		Domain string `json:"domain"`
	}
	if !s.bindJSON(c, &in) {
		return
	}
	d := spam.NormalizeDomain(in.Domain)
	if d == "" || len(d) > 253 || strings.ContainsAny(d, "/:@ ") || !strings.Contains(d, ".") {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if err := s.Store.AddSpamDomain(c.Request.Context(), d, currentUser(c).ID); err != nil {
		s.storeError(c, err)
		return
	}
	s.spamMu.Lock()
	s.spamDomains = nil
	s.spamMu.Unlock()
	c.Status(http.StatusNoContent)
}

// adminRemoveSpamDomain is DELETE /api/v1/admin/spam-domains/{domain}.
func (s *Server) adminRemoveSpamDomain(c *gin.Context) {
	if err := s.Store.RemoveSpamDomain(c.Request.Context(), spam.NormalizeDomain(c.Param("domain"))); err != nil {
		s.storeError(c, err)
		return
	}
	s.spamMu.Lock()
	s.spamDomains = nil
	s.spamMu.Unlock()
	c.Status(http.StatusNoContent)
}
