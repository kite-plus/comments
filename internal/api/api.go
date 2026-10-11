// Package api serves the comment service's HTTP interface with Gin: the
// widget's anonymous reads and writes, the login protocol, the reader
// center and site console APIs, the platform tools, the identity service's
// callbacks and the pages mail links open. docs/design/api.md is the
// contract.
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/verify"
)

// Server holds what the handlers need.
type Server struct {
	Store   *store.Store
	Keys    *crypt.Keys
	OIDC    *oidc.Client
	Fetcher *verify.Fetcher
	// PublicURL is the service's own origin, such as
	// https://comments.kite.plus.
	PublicURL string
	// Issuer is the identity service's issuer, the key half of (iss, sub).
	Issuer           string
	IDPWebhookSecret string
	TrustedProxies   []string
	// DevHTTPOrigins accepts http site origins (config.DevHTTPOrigins).
	DevHTTPOrigins bool
	// Widget and Console are the built frontends (package web).
	Widget  fs.FS
	Console fs.FS
	Log     *slog.Logger
	Now     func() time.Time

	origin    string
	secure    bool
	issuerOrg string
	assets    *widgetAssets

	anonReads    *limiter
	loginStarts  *limiter
	redeems      *limiter
	memberSite   *limiter
	memberAll    *limiter
	guestPosts   *limiter
	userThreads  *limiter
	guestThreads *limiter
	reports      *limiter
	verifies     *limiter
	claims       *limiter
	writes       *limiter

	spamMu      sync.Mutex
	spamDomains map[string]bool
	spamLoaded  time.Time
}

// Handler builds the router.
func (s *Server) Handler() (http.Handler, error) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	s.origin = strings.TrimRight(s.PublicURL, "/")
	s.secure = strings.HasPrefix(s.origin, "https://")
	if org, err := originOf(s.Issuer); err == nil {
		s.issuerOrg = org
	}
	s.assets = loadWidgetAssets(s.Widget, s.Log)

	s.anonReads = newLimiter(policy.AnonymousReadsPerIP, s.Now)
	s.loginStarts = newLimiter(policy.LoginStartsPerIP, s.Now)
	s.redeems = newLimiter(policy.RedeemsPerIP, s.Now)
	s.memberSite = newLimiter(policy.MemberPostsPerSite, s.Now)
	s.memberAll = newLimiter(policy.MemberPostsOverall, s.Now)
	s.guestPosts = newLimiter(policy.GuestPostsPerIP, s.Now)
	s.userThreads = newLimiter(policy.NewThreadsPerUser, s.Now)
	s.guestThreads = newLimiter(policy.NewThreadsPerGuest, s.Now)
	s.reports = newLimiter(policy.ReportsPerUser, s.Now)
	s.verifies = newLimiter(policy.VerifyPerSite, s.Now)
	s.claims = newLimiter(policy.ClaimMailsPerUser, s.Now)
	s.writes = newLimiter(policy.Rate{Max: 120, Window: time.Minute}, s.Now)
	if s.DevHTTPOrigins {
		// On a development machine every browser tab shares one address.
		s.anonReads = newLimiter(policy.Rate{Max: 100000, Window: time.Minute}, s.Now)
		s.loginStarts = newLimiter(policy.Rate{Max: 10000, Window: time.Minute}, s.Now)
		s.redeems = newLimiter(policy.Rate{Max: 100000, Window: time.Minute}, s.Now)
	}

	// Release mode keeps Gin's startup chatter out of the logs, and gin.New
	// rather than gin.Default leaves out the logger that writes addresses.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Match on the raw path so an origin can be a percent-encoded segment.
	r.UseRawPath = true
	r.UnescapePathValues = true
	if err := r.SetTrustedProxies(s.TrustedProxies); err != nil {
		return nil, err
	}
	r.Use(s.logRequests(), s.recover(), securityHeaders)
	r.NoRoute(func(c *gin.Context) { s.fail(c, http.StatusNotFound, codeNotFound) })

	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/readyz", s.ready)

	// Anonymous reads: no cookie gets past noCookies (api.md section 3).
	// Static files are not counted: a page with many readers behind one
	// address loads them often, and they cost nothing.
	anon := r.Group("/", noCookies)
	reads := s.limit(s.anonReads)
	anon.GET("/embed.js", s.embedJS)
	anon.GET("/embed/*path", reads, s.embedPath)
	anon.GET("/static/widget/*file", s.widgetFile)
	anon.GET("/avatars/*path", s.avatar)
	anon.GET("/api/v1/threads/comments", reads, s.threadComments)
	anon.GET("/api/v1/comments/:id/replies", reads, s.replies)
	anon.GET("/api/v1/embed-info", s.embedInfo)

	// The reader center and site console (web/console).
	r.GET("/static/console/*file", s.consoleFile)
	for _, p := range []string{"/me", "/me/*any", "/console", "/console/*any"} {
		r.GET(p, s.consoleIndex)
	}

	// Login (api.md section 4).
	r.GET("/auth/popup", s.popupPage)
	r.POST("/auth/transactions", s.limit(s.loginStarts), s.createTransaction)
	r.POST("/auth/redirect", s.limit(s.loginStarts), s.redirectLogin)
	r.GET("/auth/callback", s.callback)
	r.POST("/auth/confirm", s.confirm)
	r.GET("/auth/login", s.limit(s.loginStarts), s.consoleLogin)
	r.POST("/auth/logout", s.consoleUser(false), s.consoleLogout)
	r.POST("/auth/backchannel-logout", s.backchannelLogout)
	r.POST("/hooks/idp", s.idpHook)

	// Widget writes and the manager's reads (api.md section 5).
	w := r.Group("/api/v1", s.sameSiteWrite)
	w.POST("/widget/sessions", s.limit(s.redeems), s.redeem)
	w.GET("/widget/viewer", s.viewer)
	w.DELETE("/widget/session", s.signOutSite)
	w.GET("/widget/threads/comments", s.managerComments)
	w.POST("/comments", s.limit(s.writes), s.postComment)
	w.PATCH("/comments/:id", s.limit(s.writes), s.editComment)
	w.DELETE("/comments/:id", s.limit(s.writes), s.deleteComment)
	w.POST("/comments/:id/reports", s.limit(s.writes), s.reportComment)
	w.POST("/comments/:id/moderation", s.limit(s.writes), s.moderateComment)
	w.PUT("/threads/lock", s.limit(s.writes), s.lockThread)
	w.POST("/preview", s.limit(s.writes), s.preview)
	w.GET("/pow/challenge", s.limit(s.writes), s.powChallenge)

	// Reader center (api.md section 6).
	me := r.Group("/api/v1/me", s.consoleUser(false))
	me.GET("", s.me)
	me.PATCH("", s.updateMe)
	me.DELETE("", s.deleteMe)
	me.GET("/avatar", s.myAvatar)
	me.GET("/comments", s.myComments)
	me.GET("/notifications", s.myNotifications)
	me.POST("/notifications/read", s.markRead)
	me.GET("/mutes", s.myMutes)
	me.PUT("/mutes/:thread", s.mute)
	me.DELETE("/mutes/:thread", s.unmute)
	me.GET("/site-sessions", s.mySiteSessions)
	me.DELETE("/site-sessions/:id", s.endSiteSession)
	me.DELETE("/site-sessions", s.endAllSiteSessions)
	me.GET("/claims/suggestion", s.claimSuggestion)
	me.POST("/claims", s.startClaim)
	me.POST("/claims/:id/verify", s.verifyClaim)
	me.POST("/claims/:id/confirm", s.confirmClaim)
	me.GET("/export", s.exportMe)

	// Site console (api.md section 7).
	sites := r.Group("/api/v1/sites", s.consoleUser(false))
	sites.GET("", s.listSites)
	sites.POST("", s.createSite)
	site := sites.Group("/:site", s.siteMember)
	site.GET("", s.getSite)
	site.PATCH("", s.ownerOnly, s.updateSite)
	site.DELETE("", s.ownerOnly, s.deleteSite)
	site.POST("/restore", s.ownerOnly, s.restoreSite)
	site.POST("/origins", s.ownerOnly, s.addOrigin)
	site.DELETE("/origins/:origin", s.ownerOnly, s.removeOrigin)
	site.POST("/verification", s.ownerOnly, s.newVerification)
	site.POST("/verification/check", s.ownerOnly, s.checkVerification)
	site.GET("/comments", s.queue)
	site.POST("/comments/moderation", s.bulkModerate)
	site.GET("/threads", s.listThreads)
	site.PATCH("/threads/:thread", s.updateThread)
	site.POST("/threads/:thread/move", s.ownerOnly, s.moveThread)
	site.GET("/bans", s.listBans)
	site.POST("/bans", s.createBan)
	site.DELETE("/bans/:id", s.deleteBan)
	site.GET("/reports", s.listReports)
	site.PATCH("/reports/:id", s.resolveReport)
	site.GET("/events", s.listEvents)
	site.POST("/events/:id/undo", s.ownerOnly, s.undoEvent)
	site.POST("/imports", s.ownerOnly, s.uploadImport)
	site.GET("/imports/:id", s.ownerOnly, s.getImport)
	site.PUT("/imports/:id/mapping", s.ownerOnly, s.setImportMapping)
	site.POST("/imports/:id/run", s.ownerOnly, s.runImport)
	site.GET("/export", s.ownerOnly, s.exportSite)

	// Platform (api.md section 8).
	admin := r.Group("/api/v1/admin", s.consoleUser(true))
	admin.GET("/sites", s.adminSites)
	admin.POST("/sites/:site/approve", s.adminApprove)
	admin.POST("/sites/:site/suspend", s.adminSuspend)
	admin.POST("/sites/:site/unsuspend", s.adminUnsuspend)
	admin.GET("/reports", s.adminReports)
	admin.PATCH("/reports/:id", s.adminResolveReport)
	admin.POST("/comments/:id/hide", s.adminHide)
	admin.POST("/users/:id/suspend", s.adminSuspendUser)
	admin.POST("/users/:id/unsuspend", s.adminUnsuspendUser)
	admin.GET("/bans", s.adminBans)
	admin.POST("/bans", s.adminCreateBan)
	admin.DELETE("/bans/:id", s.adminDeleteBan)
	admin.GET("/spam-domains", s.adminSpamDomains)
	admin.POST("/spam-domains", s.adminAddSpamDomain)
	admin.DELETE("/spam-domains/:domain", s.adminRemoveSpamDomain)

	// Pages that mail links open; GET shows, POST acts (api.md section 9).
	r.GET("/unsubscribe", s.unsubscribePage)
	r.POST("/unsubscribe", s.unsubscribe)
	r.GET("/mute", s.mutePage)
	r.POST("/mute", s.muteByMail)
	r.GET("/guest-email/confirm", s.guestConfirmPage)
	r.POST("/guest-email/confirm", s.guestConfirm)
	return r, nil
}

// securityHeaders applies to every response; pages add their own CSP.
func securityHeaders(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Next()
}

func (s *Server) ready(c *gin.Context) {
	if err := s.Store.Ping(c.Request.Context()); err != nil {
		s.Log.Error("database unreachable", "error", err)
		c.String(http.StatusServiceUnavailable, "database unreachable")
		return
	}
	c.String(http.StatusOK, "ok")
}
