package api

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// widgetAssets is what the server needs from web/widget/dist: the loader,
// the iframe app's script and its CSS to inline, and the popup's script.
type widgetAssets struct {
	fs           fs.FS
	ok           bool
	embedJS      []byte
	embedSRI     string
	embedVersion string
	appJS        string // path under /static/widget/
	appCSS       string
	cssHash      string // for the CSP's style-src
	popupJS      string
}

type manifestEntry struct {
	File string   `json:"file"`
	CSS  []string `json:"css"`
}

func loadWidgetAssets(fsys fs.FS, log *slog.Logger) *widgetAssets {
	a := &widgetAssets{fs: fsys}
	if fsys == nil {
		return a
	}
	raw, err := fs.ReadFile(fsys, ".vite/manifest.json")
	if err != nil {
		log.Warn("widget assets are not built; run make web", "error", err)
		return a
	}
	var manifest map[string]manifestEntry
	if err := json.Unmarshal(raw, &manifest); err != nil {
		log.Error("widget manifest", "error", err)
		return a
	}
	app, popup := manifest["src/app/main.tsx"], manifest["src/popup/popup.ts"]
	a.appJS, a.popupJS = app.File, popup.File
	if len(app.CSS) > 0 {
		css, err := fs.ReadFile(fsys, app.CSS[0])
		if err != nil {
			log.Error("widget css", "error", err)
			return a
		}
		a.appCSS = strings.TrimSpace(string(css))
		sum := sha256.Sum256([]byte(a.appCSS))
		a.cssHash = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	if a.embedJS, err = fs.ReadFile(fsys, "embed.js"); err != nil {
		log.Error("embed.js", "error", err)
		return a
	}
	sum := sha512.Sum384(a.embedJS)
	a.embedSRI = "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
	var build struct {
		EmbedVersion string `json:"embed_version"`
	}
	if b, err := fs.ReadFile(fsys, "build.json"); err == nil && json.Unmarshal(b, &build) == nil {
		a.embedVersion = build.EmbedVersion
	}
	a.ok = a.appJS != "" && a.popupJS != ""
	return a
}

func (s *Server) embedJS(c *gin.Context) {
	if len(s.assets.embedJS) == 0 {
		s.fail(c, http.StatusServiceUnavailable, codeUnavailable)
		return
	}
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", s.assets.embedJS)
}

var pinnedName = regexp.MustCompile(`^v1\.\d+\.\d+\.js$`)

// embedPath serves /embed/v1/thread, the iframe document, and the pinned
// loaders /embed/v1.<minor>.<patch>.js.
func (s *Server) embedPath(c *gin.Context) {
	p := strings.TrimPrefix(c.Param("path"), "/")
	if p == "v1/thread" {
		s.threadDocument(c)
		return
	}
	if !pinnedName.MatchString(p) || s.assets.fs == nil {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	b, err := fs.ReadFile(s.assets.fs, "embed/"+p)
	if err != nil {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return
	}
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", b)
}

// embedInfo tells the console what to show for the pinned, SRI-checked
// way of embedding (widget.md section 10).
func (s *Server) embedInfo(c *gin.Context) {
	if s.assets.embedVersion == "" {
		s.fail(c, http.StatusServiceUnavailable, codeUnavailable)
		return
	}
	cached(c, 300e9, "application/json; charset=utf-8", encodeJSON(gin.H{
		"version":    s.assets.embedVersion,
		"script_url": s.origin + "/embed.js",
		"pinned_url": s.origin + "/embed/v" + s.assets.embedVersion + ".js",
		"integrity":  s.assets.embedSRI,
	}))
}

func (s *Server) widgetFile(c *gin.Context) {
	serveFile(c, s.assets.fs, strings.TrimPrefix(c.Param("file"), "/"), "public, max-age=31536000, immutable")
}

func (s *Server) consoleFile(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("file"), "/")
	cache := "public, max-age=300"
	if strings.HasPrefix(name, "assets/") {
		cache = "public, max-age=31536000, immutable"
	}
	serveFile(c, s.Console, name, cache)
}

func serveFile(c *gin.Context, fsys fs.FS, name, cacheControl string) {
	if fsys == nil || name == "" || strings.Contains(name, "..") || strings.HasPrefix(path.Base(name), ".") {
		c.Status(http.StatusNotFound)
		return
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Cache-Control", cacheControl)
	c.Data(http.StatusOK, ct, b)
}

// consoleCSP is the policy for the reader center and console: same-origin
// scripts only, never framed. Inline styles are allowed for the component
// library's positioning.
const consoleCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// consoleIndex serves the single page app for /me and /console routes.
func (s *Server) consoleIndex(c *gin.Context) {
	c.Header("Content-Security-Policy", consoleCSP)
	c.Header("Cache-Control", "no-cache")
	if s.Console != nil {
		if b, err := fs.ReadFile(s.Console, "index.html"); err == nil {
			c.Data(http.StatusOK, "text/html; charset=utf-8", b)
			return
		}
	}
	c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8",
		[]byte(`<!doctype html><meta charset="utf-8"><title>Kite Plus Comments</title><p>The reader center and console are not built in this binary.</p>`))
}
