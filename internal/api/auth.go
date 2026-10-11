package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/idp"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
)

// popupPage is GET /auth/popup, the login popup's first page. Its script
// says ready to the opener, takes begin {site, challenge} only from the
// opener at the service's own origin, creates the transaction and goes to
// the identity service (login.md section 4.2).
func (s *Server) popupPage(c *gin.Context) {
	s.writePage(c, pageOpts{
		title:  t(c, "登录 Kite Plus 评论", "Sign in to Kite Plus Comments"),
		script: s.assets.popupJS,
		data:   map[string]string{"origin": s.origin, "step": "begin"},
		body: `<h1>` + esc(t(c, "正在连接 Kite Plus 账号……", "Connecting to your Kite Plus account…")) + `</h1>` +
			`<p id="kc-no-opener" hidden>` + esc(t(c, "这个窗口和文章失去了联系。请关闭它，回到文章里再点一次登录。",
			"This window lost touch with the article. Close it and sign in from the article again.")) + `</p>` +
			`<p id="kc-failed" hidden>` + esc(t(c, "登录没能开始，请关闭这个窗口后重试。", "Signing in could not start. Close this window and try again.")) + `</p>`,
	})
}

// decodeChallenge reads a base64url SHA-256 challenge.
func decodeChallenge(s string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return b, err == nil && len(b) == 32
}

// binder returns the browser's binder, renewing its cookie, or a new one
// (login.md section 4.5).
func (s *Server) binder(c *gin.Context) string {
	name := s.cookieName("comments_binder")
	v, err := c.Cookie(name)
	if err != nil || len(v) != 43 {
		v = crypt.Token()
	}
	s.setCookie(c, name, v, policy.BinderTTL, http.SameSiteLaxMode, false)
	return v
}

func (s *Server) binderHash(c *gin.Context) []byte {
	v, err := c.Cookie(s.cookieName("comments_binder"))
	if err != nil || len(v) != 43 {
		return nil
	}
	return crypt.Hash(v)
}

// startLogin records a transaction and returns the authorization URL.
func (s *Server) startLogin(c *gin.Context, tx store.LoginTx) (string, error) {
	state, nonce, verifier := crypt.Token(), crypt.Token(), crypt.Token()
	authURL, err := s.OIDC.AuthURL(c.Request.Context(), state, nonce, verifier)
	if err != nil {
		return "", err
	}
	tx.StateHash = crypt.Hash(state)
	tx.BinderHash = crypt.Hash(s.binder(c))
	tx.Nonce = nonce
	tx.PKCEVerifier = verifier
	tx.ExpiresAt = s.Now().Add(policy.LoginTransactionTTL)
	if _, err := s.Store.CreateLoginTx(c.Request.Context(), tx); err != nil {
		return "", err
	}
	return authURL, nil
}

// createTransaction is POST /auth/transactions from the popup's fetch.
func (s *Server) createTransaction(c *gin.Context) {
	if !s.loginEntry(c) || c.GetHeader("Sec-Fetch-Mode") == "navigate" {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return
	}
	var body struct {
		Site      string `json:"site"`
		Challenge string `json:"challenge"`
	}
	if !s.bindJSON(c, &body) {
		return
	}
	challenge, ok := decodeChallenge(body.Challenge)
	if !ok {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	site, err := s.Store.Site(c.Request.Context(), body.Site)
	if err != nil || site.Status != model.SiteActive {
		s.fail(c, http.StatusForbidden, codeSiteInactive)
		return
	}
	authURL, err := s.startLogin(c, store.LoginTx{Mode: store.ModePopup, SiteID: &site.ID, Challenge: challenge})
	if err != nil {
		s.loginError(c, err)
		return
	}
	private(c, http.StatusOK, gin.H{"authorize_url": authURL})
}

func (s *Server) loginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		s.fail(c, http.StatusConflict, codeConflict)
	case errors.Is(err, oidc.ErrUnavailable):
		s.Log.Warn("identity service unavailable", "error", err)
		s.fail(c, http.StatusServiceUnavailable, codeUnavailable)
	default:
		s.storeError(c, err)
	}
}

// redirectLogin is POST /auth/redirect, the full-page fallback, submitted
// by the widget's form with target=_top (login.md section 4.4).
func (s *Server) redirectLogin(c *gin.Context) {
	if !s.loginEntry(c) {
		s.messagePage(c, http.StatusForbidden, t(c, "无法登录", "Cannot sign in"),
			t(c, "登录只能从评论区发起。", "Sign-in can only start from the comment section."))
		return
	}
	challenge, ok := decodeChallenge(c.PostForm("challenge"))
	site, err := s.Store.Site(c.Request.Context(), c.PostForm("site"))
	if !ok || err != nil || site.Status != model.SiteActive {
		s.messagePage(c, http.StatusBadRequest, t(c, "无法登录", "Cannot sign in"),
			t(c, "这个站点的评论暂不可用。", "Comments are unavailable on this site."))
		return
	}
	ret, ok := s.returnURL(site, c.PostForm("return"))
	if !ok {
		s.messagePage(c, http.StatusBadRequest, t(c, "无法登录", "Cannot sign in"),
			t(c, "返回地址不属于这个站点。", "The return address does not belong to this site."))
		return
	}
	authURL, err := s.startLogin(c, store.LoginTx{Mode: store.ModeRedirect, SiteID: &site.ID, Challenge: challenge, ReturnTo: ret})
	if err != nil {
		if errors.Is(err, oidc.ErrUnavailable) || errors.Is(err, store.ErrConflict) {
			s.messagePage(c, http.StatusServiceUnavailable, t(c, "无法登录", "Cannot sign in"),
				t(c, "暂时连不上 Kite Plus 账号，请稍后再试。", "The Kite Plus account service is unreachable; try again later."))
			return
		}
		s.storeError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, authURL)
}

// returnURL accepts an absolute address on one of the site's verified
// origins, without its fragment, against open redirects.
func (s *Server) returnURL(site model.Site, raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || utf8.RuneCountInString(raw) > policy.ThreadURLRunes {
		return "", false
	}
	origin, err := originOf(raw)
	if err != nil || !site.HasVerifiedOrigin(origin) || (!strings.HasPrefix(origin, "https://") && !s.DevHTTPOrigins) {
		return "", false
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String(), true
}

// callback is GET /auth/callback, where the identity service returns.
func (s *Server) callback(c *gin.Context) {
	ctx := c.Request.Context()
	failed := t(c, "登录没有完成", "Sign-in did not finish")
	tx, err := s.Store.LoginTxByState(ctx, crypt.Hash(c.Query("state")))
	binder := s.binderHash(c)
	if err != nil || binder == nil || !bytes.Equal(binder, tx.BinderHash) {
		// No transaction, or one started in another browser: a forged or
		// replayed callback (login.md section 4.3).
		s.messagePage(c, http.StatusBadRequest, failed,
			t(c, "这个登录链接无效。请回到评论区重新登录。", "This sign-in link is not valid. Sign in again from the comment section."))
		return
	}
	if tx.Status != "pending" || !tx.ExpiresAt.After(s.Now()) {
		s.messagePage(c, http.StatusGone, failed, t(c, "登录已过期，请重新登录。", "The sign-in expired. Sign in again."))
		return
	}
	if c.Query("error") != "" || c.Query("code") == "" {
		s.messagePage(c, http.StatusBadRequest, failed, t(c, "登录被取消了。", "Sign-in was canceled."))
		return
	}
	claims, err := s.OIDC.Exchange(ctx, c.Query("code"), tx.PKCEVerifier, tx.Nonce)
	if err != nil {
		s.Log.Warn("oidc exchange failed", "error", err)
		s.messagePage(c, http.StatusBadGateway, failed, t(c, "无法确认你的身份，请重新登录。", "Your identity could not be confirmed. Sign in again."))
		return
	}
	login := store.OIDCLogin{Issuer: s.Issuer, Subject: claims.Subject, DisplayName: displayName(claims),
		Locale: store.LocaleFromClaim(claims.Locale)}
	if claims.EmailVerified && claims.Email != "" {
		login.EmailEnc = s.Keys.Encrypt(crypt.NormalizeEmail(claims.Email))
		login.EmailHMAC = s.Keys.EmailHMAC(claims.Email)
	}
	u, created, err := s.Store.LoginUser(ctx, login)
	if err != nil {
		s.storeError(c, err)
		return
	}
	if u.SuspendedAt != nil {
		s.messagePage(c, http.StatusForbidden, failed, t(c, "你的账号已被暂停。", "Your account is suspended."))
		return
	}
	if err := s.Store.AuthenticateLoginTx(ctx, tx.ID, u.ID, claims.SID); err != nil {
		s.messagePage(c, http.StatusGone, failed, t(c, "登录已过期，请重新登录。", "The sign-in expired. Sign in again."))
		return
	}
	if claims.Picture != "" && (created || !u.HasAvatar) {
		go s.fetchAvatar(u.ID, claims.Picture, created)
	}
	switch tx.Mode {
	case store.ModePopup:
		s.writePage(c, pageOpts{
			title:  t(c, "登录成功", "Signed in"),
			script: s.assets.popupJS,
			data:   map[string]string{"origin": s.origin, "step": "done"},
			body: `<h1>` + esc(t(c, "登录成功", "Signed in")) + `</h1><p>` +
				esc(t(c, "可以关闭这个窗口，回到文章里继续。", "You can close this window and go back to the article.")) + `</p>`,
		})
	case store.ModeRedirect:
		s.confirmPage(c, tx, u)
	case store.ModeConsole:
		token := crypt.Token()
		if err := s.Store.CreateConsoleSession(ctx, tx.ID, crypt.Hash(token), u.ID, claims.SID, s.Now().Add(policy.ConsoleSessionTTL)); err != nil {
			s.storeError(c, err)
			return
		}
		s.setCookie(c, s.cookieName("comments_session"), token, policy.ConsoleSessionTTL, http.SameSiteLaxMode, false)
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusSeeOther, tx.ReturnTo)
	}
}

// confirmPage asks the reader to confirm the site they return to, the
// second line of defense of the redirect flow (login.md section 4.4).
func (s *Server) confirmPage(c *gin.Context, tx store.LoginTx, u model.User) {
	site, err := s.Store.SiteByID(c.Request.Context(), *tx.SiteID)
	if err != nil {
		s.storeError(c, err)
		return
	}
	host := tx.ReturnTo
	if ru, err := url.Parse(tx.ReturnTo); err == nil {
		host = ru.Host
	}
	origin, _ := originOf(tx.ReturnTo)
	text := "以 <strong>" + esc(u.DisplayName) + "</strong> 的身份回到「" + esc(site.Name) + "」（" + esc(host) + "）？"
	if !zh(c) {
		text = "Go back to “" + esc(site.Name) + "” (" + esc(host) + ") as <strong>" + esc(u.DisplayName) + "</strong>?"
	}
	s.writePage(c, pageOpts{
		title:       t(c, "确认登录", "Confirm sign-in"),
		formTargets: []string{origin},
		body: `<h1>` + esc(t(c, "确认登录", "Confirm sign-in")) + `</h1><p>` + text + `</p>` +
			`<form method="post" action="/auth/confirm"><input type="hidden" name="tx" value="` + strconv.FormatInt(tx.ID, 10) + `">` +
			`<button class="primary" type="submit">` + esc(t(c, "确认", "Confirm")) + `</button></form>` +
			`<p>` + esc(t(c, "如果你没有在这个网站上点过登录，关掉这个页面即可。", "If you did not sign in on that site, just close this page.")) + `</p>`,
	})
}

// confirm is POST /auth/confirm.
func (s *Server) confirm(c *gin.Context) {
	id, err := strconv.ParseInt(c.PostForm("tx"), 10, 64)
	binder := s.binderHash(c)
	if !s.loginEntry(c) || err != nil || binder == nil {
		s.messagePage(c, http.StatusForbidden, t(c, "无法确认", "Cannot confirm"), t(c, "这个确认无效。", "This confirmation is not valid."))
		return
	}
	tx, err := s.Store.ConfirmLoginTx(c.Request.Context(), id, binder)
	if err != nil {
		s.messagePage(c, http.StatusGone, t(c, "无法确认", "Cannot confirm"),
			t(c, "登录已过期或已经确认过。请回到评论区重新登录。", "The sign-in expired or was confirmed already. Sign in again from the comment section."))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusSeeOther, tx.ReturnTo+"#kc-login")
}

// consoleLogin is GET /auth/login, the first-party sign-in of the reader
// center and console.
func (s *Server) consoleLogin(c *gin.Context) {
	ret := c.Query("return")
	if !strings.HasPrefix(ret, "/") || strings.HasPrefix(ret, "//") || strings.ContainsAny(ret, "\\\r\n") || len(ret) > 500 {
		ret = "/me"
	}
	authURL, err := s.startLogin(c, store.LoginTx{Mode: store.ModeConsole, ReturnTo: ret})
	if err != nil {
		s.messagePage(c, http.StatusServiceUnavailable, t(c, "无法登录", "Cannot sign in"),
			t(c, "暂时连不上 Kite Plus 账号，请稍后再试。", "The Kite Plus account service is unreachable; try again later."))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, authURL)
}

// consoleLogout is POST /auth/logout.
func (s *Server) consoleLogout(c *gin.Context) {
	token := c.GetString("session_token")
	if err := s.Store.DeleteConsoleSession(c.Request.Context(), crypt.Hash(token)); err != nil {
		s.storeError(c, err)
		return
	}
	s.setCookie(c, s.cookieName("comments_session"), "", -1, http.SameSiteLaxMode, false)
	c.Status(http.StatusNoContent)
}

// backchannelLogout is POST /auth/backchannel-logout: end what one
// identity service session issued (login.md section 6).
func (s *Server) backchannelLogout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	lo, err := s.OIDC.VerifyLogoutToken(c.Request.Context(), c.PostForm("logout_token"))
	if err != nil {
		s.Log.Warn("back-channel logout rejected", "error", err)
		writeJSON(c, http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	ctx := c.Request.Context()
	if lo.SID != "" {
		n, err := s.Store.DeleteSessionsBySID(ctx, lo.SID)
		if err != nil {
			s.storeError(c, err)
			return
		}
		s.Log.Info("back-channel logout", "sessions", n)
	} else if u, err := s.Store.UserBySubject(ctx, s.Issuer, lo.Subject); err == nil {
		if err := s.Store.DeleteUserSessions(ctx, u.ID); err != nil {
			s.storeError(c, err)
			return
		}
	}
	c.Status(http.StatusOK)
}

// idpHook is POST /hooks/idp, the identity service's user events.
func (s *Server) idpHook(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	if err := idp.VerifySignature(c.GetHeader(idp.SignatureHeader), body, s.IDPWebhookSecret, s.Now()); err != nil {
		s.fail(c, http.StatusUnauthorized, codeUnauthorized)
		return
	}
	ev, err := idp.ParseWebhook(body)
	if err != nil {
		s.Log.Warn("identity event unreadable", "error", err)
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return
	}
	applied, err := idp.Apply(c.Request.Context(), s.Store, s.Issuer, ev)
	if err != nil {
		s.storeError(c, err)
		return
	}
	s.Log.Info("identity event", "type", ev.Type, "applied", applied)
	c.Status(http.StatusOK)
}

// displayName picks a new account's name (data-model.md section 3.1).
func displayName(cl oidc.Claims) string {
	for _, n := range []string{cl.Name, cl.PreferredUsername} {
		if n = spam.CleanName(n); n != "" {
			return truncate(n, policy.DisplayNameRunes)
		}
	}
	return "读者" + strings.ToUpper(base64.RawURLEncoding.EncodeToString(crypt.Random(3)))[:4]
}

// fetchAvatar copies the identity service's picture once, resized to
// 96x96, so pages never load it from elsewhere (data-model.md section 2).
func (s *Server) fetchAvatar(userID int64, pictureURL string, adopt bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, _, err := s.Fetcher.Get(ctx, pictureURL, 5<<20)
	if err != nil {
		s.Log.Info("avatar not fetched", "error", err)
		return
	}
	img, ct, etag, err := resizeAvatar(raw)
	if err != nil {
		s.Log.Info("avatar not usable", "error", err)
		return
	}
	if err := s.Store.SetAvatar(ctx, userID, ct, img, etag); err != nil {
		s.Log.Error("avatar", "error", err)
		return
	}
	if adopt {
		kind := "idp"
		if _, err := s.Store.UpdateProfile(ctx, userID, store.ProfileUpdate{AvatarKind: &kind}); err != nil {
			s.Log.Error("avatar", "error", err)
		}
	}
}
