package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

// logRequests records the route template, never the client address, the
// user agent or the query string (login.md section 8, item 12).
func (s *Server) logRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		s.Log.Info("request", "method", c.Request.Method, "route", route,
			"status", c.Writer.Status(), "duration", time.Since(start))
	}
}

func (s *Server) recover() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		s.Log.Error("panic", "error", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		s.fail(c, http.StatusInternalServerError, codeInternal)
	})
}

// noCookies drops the Cookie header before anything can read it: the iframe
// document, anonymous reads, avatars and embed.js never see a session. The
// reverse proxy strips it first (deploy/Caddyfile); this is the second
// layer (widget.md section 4, item 5).
func noCookies(c *gin.Context) {
	c.Request.Header.Del("Cookie")
	c.Next()
}

// limit applies a per-client limit. The address is used only as a key in
// memory.
func (s *Server) limit(l *limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, retry := l.allow(c.ClientIP()); !ok {
			s.tooMany(c, retry)
			return
		}
		c.Next()
	}
}

func (s *Server) tooMany(c *gin.Context, retry time.Duration) {
	c.Header("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	s.fail(c, http.StatusTooManyRequests, codeRateLimited)
}

// sameSiteWrite guards every state-changing widget call: the host page
// shares the partitioned cookie's jar, so a request must come from the
// service's own pages (login.md section 5).
func (s *Server) sameSiteWrite(c *gin.Context) {
	if !s.fromOwnPage(c) {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	c.Next()
}

// fromOwnPage: Sec-Fetch-Site must be same-origin when present; without
// it, Origin must be the service's own.
func (s *Server) fromOwnPage(c *gin.Context) bool {
	if site := c.GetHeader("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	return c.GetHeader("Origin") == s.origin
}

// loginEntry guards the endpoints that create login transactions and the
// confirmation: only the service's own pages may call them (login.md
// section 4.1). Origin is required; Sec-Fetch-Site, when sent, must be
// same-origin.
func (s *Server) loginEntry(c *gin.Context) bool {
	if c.GetHeader("Origin") != s.origin {
		return false
	}
	if site := c.GetHeader("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return true
}

// Cookie names: __Host- on https, plain names on a plain http development
// server where browsers refuse the prefix.
func (s *Server) cookieName(base string) string {
	if s.secure {
		return "__Host-" + base
	}
	return base
}

func (s *Server) siteCookieName(sitePublicID string) string {
	return s.cookieName("comments_site_" + sitePublicID)
}

func (s *Server) setCookie(c *gin.Context, name, value string, maxAge time.Duration, sameSite http.SameSite, partitioned bool) {
	ck := &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()), HttpOnly: true,
		Secure: s.secure, SameSite: sameSite, Partitioned: partitioned}
	if maxAge < 0 {
		ck.MaxAge = -1
	}
	// SameSite=None and Partitioned need Secure; browsers accept Secure
	// from http://localhost, which is where a plain http server runs.
	if sameSite == http.SameSiteNoneMode || partitioned {
		ck.Secure = true
	}
	http.SetCookie(c.Writer, ck)
}

// consoleUser resolves the console session; writes also need its CSRF
// token, derived from the session as in Explore.
func (s *Server) consoleUser(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		token, err := c.Cookie(s.cookieName("comments_session"))
		if err != nil || len(token) != 43 {
			s.fail(c, http.StatusUnauthorized, codeUnauthorized)
			return
		}
		u, err := s.Store.ConsoleUser(c.Request.Context(), crypt.Hash(token))
		if err != nil {
			s.fail(c, http.StatusUnauthorized, codeUnauthorized)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead &&
			subtle.ConstantTimeCompare([]byte(c.GetHeader("X-CSRF-Token")), []byte(csrfToken(token))) != 1 {
			s.fail(c, http.StatusForbidden, codeForbidden)
			return
		}
		if admin && !u.IsPlatformAdmin {
			s.fail(c, http.StatusForbidden, codeForbidden)
			return
		}
		c.Set("user", u)
		c.Set("session_token", token)
		c.Next()
	}
}

func currentUser(c *gin.Context) model.User {
	u, _ := c.Get("user")
	return u.(model.User)
}

func csrfToken(token string) string {
	hash := sha256.Sum256([]byte("csrf:" + token))
	return hex.EncodeToString(hash[:])
}

// limiter counts requests per key in fixed windows, in memory only
// (moderation.md section 3.4).
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu     sync.Mutex
	counts map[string]window
	calls  int
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(r policy.Rate, now func() time.Time) *limiter {
	return &limiter{max: r.Max, window: r.Window, now: now, counts: make(map[string]window)}
}

func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%1024 == 0 {
		for k, w := range l.counts {
			if now.Sub(w.start) >= l.window {
				delete(l.counts, k)
			}
		}
	}
	w := l.counts[key]
	if now.Sub(w.start) >= l.window {
		w = window{start: now}
	}
	if w.n >= l.max {
		return false, w.start.Add(l.window).Sub(now)
	}
	w.n++
	l.counts[key] = w
	return true, 0
}

// count returns how many requests the key made in the current window.
func (l *limiter) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.counts[key]
	if l.now().Sub(w.start) >= l.window {
		return 0
	}
	return w.n
}

// clientLabel sums up a user agent as "Browser · OS" for the reader center;
// the user agent itself is not stored (data-model.md section 3.2).
func clientLabel(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "MicroMessenger"):
		browser = "WeChat"
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	osName := ""
	switch {
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		osName = "iOS"
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	if osName == "" {
		return browser
	}
	return browser + " · " + osName
}
