package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

// Error codes; see docs/design/api.md section 10.
const (
	codeInvalidRequest     = "invalid_request"
	codeCommentTooLong     = "comment_too_long"
	codeTooManyLinks       = "too_many_links"
	codeOriginNotAllowed   = "origin_not_allowed"
	codePowInvalid         = "pow_invalid"
	codeUnauthorized       = "unauthorized"
	codeForbidden          = "forbidden"
	codeSiteInactive       = "site_inactive"
	codeThreadLocked       = "thread_locked"
	codeEditWindowClosed   = "edit_window_closed"
	codeGuestNotAllowed    = "guest_not_allowed"
	codeNotFound           = "not_found"
	codeConflict           = "conflict"
	codeSoleOwner          = "sole_owner"
	codeLoginExpired       = "login_expired"
	codeVerificationFailed = "verification_failed"
	codeImportInvalid      = "import_invalid"
	codeRateLimited        = "rate_limited"
	codeInternal           = "internal"
	codeUnavailable        = "unavailable"
)

var messages = map[string]struct{ en, zh string }{
	codeInvalidRequest:     {"The request is malformed.", "请求格式不正确。"},
	codeCommentTooLong:     {"The comment is too long.", "评论太长了。"},
	codeTooManyLinks:       {"The comment has too many links.", "评论里的链接太多了。"},
	codeOriginNotAllowed:   {"This page does not belong to the site.", "此页面不属于这个站点。"},
	codePowInvalid:         {"The check did not pass; try again.", "验证没有通过，请重试。"},
	codeUnauthorized:       {"Sign in first.", "请先登录。"},
	codeForbidden:          {"You cannot do this here.", "你无法在此执行这个操作。"},
	codeSiteInactive:       {"Comments are unavailable on this site.", "这个站点的评论暂不可用。"},
	codeThreadLocked:       {"Comments are closed.", "评论已关闭。"},
	codeEditWindowClosed:   {"This comment can no longer be edited.", "这条评论已经过了可以编辑的时间。"},
	codeGuestNotAllowed:    {"This site does not take guest comments.", "这个站点不接受访客评论。"},
	codeNotFound:           {"Not found.", "没有找到。"},
	codeConflict:           {"This conflicts with existing data.", "与已有的数据冲突。"},
	codeSoleOwner:          {"You are the only owner of a site. Delete the site first.", "你是某个站点唯一的所有者，请先删除这个站点。"},
	codeLoginExpired:       {"The sign-in expired; try again.", "登录已过期，请重试。"},
	codeVerificationFailed: {"The verification record was not found.", "没有找到验证记录。"},
	codeImportInvalid:      {"The file cannot be read.", "无法解析这个文件。"},
	codeRateLimited:        {"Too many requests; try again later.", "请求太频繁，请稍后再试。"},
	codeInternal:           {"Something went wrong on our side.", "服务端出错了。"},
	codeUnavailable:        {"A service we depend on is unavailable; try again later.", "依赖的服务暂时不可用，请稍后再试。"},
}

// zh reports whether the request prefers Chinese. Accept-Language is
// matched loosely: the first zh tag before any en tag wins.
func zh(c *gin.Context) bool { return prefersChinese(c.GetHeader("Accept-Language")) }

func prefersChinese(header string) bool {
	if header == "" {
		return true
	}
	for part := range strings.SplitSeq(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "zh"):
			return true
		case strings.HasPrefix(tag, "en"):
			return false
		}
	}
	return false
}

func message(code string, chinese bool) string {
	m := messages[code]
	if chinese {
		return m.zh
	}
	return m.en
}

// fail writes an error in the reader's language and stops the chain.
func (s *Server) fail(c *gin.Context, status int, code string, detail ...string) {
	msg := message(code, zh(c))
	if len(detail) > 0 && detail[0] != "" {
		msg += " " + detail[0]
	}
	c.Header("Vary", "Accept-Language")
	c.Header("Cache-Control", "no-store")
	writeJSON(c, status, gin.H{"error": gin.H{"code": code, "message": msg}})
	c.Abort()
}

// storeError maps store errors to responses; anything unexpected is logged
// and reported as internal.
func (s *Server) storeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		// The client went away; nobody reads the answer.
		c.AbortWithStatus(499)
	case errors.Is(err, store.ErrNotFound):
		s.fail(c, http.StatusNotFound, codeNotFound)
	case errors.Is(err, store.ErrConflict):
		s.fail(c, http.StatusConflict, codeConflict)
	case errors.Is(err, store.ErrSoleOwner):
		s.fail(c, http.StatusConflict, codeSoleOwner)
	case errors.Is(err, store.ErrThreadLocked):
		s.fail(c, http.StatusForbidden, codeThreadLocked)
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrBadParent):
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
	case errors.Is(err, store.ErrLoginGone):
		s.fail(c, http.StatusGone, codeLoginExpired)
	default:
		s.Log.Error("request failed", "route", c.FullPath(), "error", err)
		s.fail(c, http.StatusInternalServerError, codeInternal)
	}
}

func encodeJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Every value passed here is built from plain types.
	_ = enc.Encode(v)
	return buf.Bytes()
}

func writeJSON(c *gin.Context, status int, v any) {
	c.Data(status, "application/json; charset=utf-8", encodeJSON(v))
}

// private writes a response that carries identity.
func private(c *gin.Context, status int, v any) {
	c.Header("Cache-Control", "private, no-store")
	writeJSON(c, status, v)
}

// cached writes a public response with a weak ETag and answers a matching
// If-None-Match with 304.
func cached(c *gin.Context, maxAge time.Duration, contentType string, body []byte) {
	sum := sha256.Sum256(body)
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", int(maxAge.Seconds())))
	c.Header("ETag", etag)
	for candidate := range strings.SplitSeq(c.GetHeader("If-None-Match"), ",") {
		if strings.TrimSpace(candidate) == etag || strings.TrimSpace(candidate) == "*" {
			c.Status(http.StatusNotModified)
			return
		}
	}
	c.Data(http.StatusOK, contentType, body)
}

// Cursors are opaque to clients: base64url of "unix-nanos:id".
func encodeCursor(cur *model.Cursor) any {
	if cur == nil {
		return nil
	}
	raw := strconv.FormatInt(cur.At.UnixNano(), 10) + ":" + strconv.FormatInt(cur.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (*model.Cursor, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	at, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, errors.New("malformed cursor")
	}
	nanos, err1 := strconv.ParseInt(at, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil {
		return nil, errors.New("malformed cursor")
	}
	return &model.Cursor{At: time.Unix(0, nanos).UTC(), ID: n}, nil
}

// page reads cursor and limit, failing the request when they are invalid.
func (s *Server) page(c *gin.Context, def, maxLimit int) (*model.Cursor, int, bool) {
	cur, err := decodeCursor(c.Query("cursor"))
	if err != nil {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return nil, 0, false
	}
	limit := def
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			s.fail(c, http.StatusBadRequest, codeInvalidRequest)
			return nil, 0, false
		}
		limit = n
	}
	return cur, limit, true
}

// idParam reads a bigint ID from the path.
func (s *Server) idParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id < 1 {
		s.fail(c, http.StatusNotFound, codeNotFound)
		return 0, false
	}
	return id, true
}

// bindJSON reads a JSON body, requiring the JSON content type.
func (s *Server) bindJSON(c *gin.Context, v any) bool {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		s.fail(c, http.StatusUnsupportedMediaType, codeInvalidRequest)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return false
	}
	return true
}

func timeOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func idString(id *int64) any {
	if id == nil {
		return nil
	}
	return strconv.FormatInt(*id, 10)
}
