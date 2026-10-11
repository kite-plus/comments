package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/model"
)

// unsubscribePage is GET /unsubscribe: opening a mail link changes
// nothing; the button posts (api.md section 9).
func (s *Server) unsubscribePage(c *gin.Context) {
	if _, err := s.Keys.Verify(mailer.PurposeUnsubscribe, c.Query("t")); err != nil {
		s.messagePage(c, http.StatusBadRequest, t(c, "链接无效", "Link not valid"), t(c, "这个退订链接无效。", "This unsubscribe link is not valid."))
		return
	}
	title := t(c, "退订邮件通知", "Unsubscribe")
	s.writePage(c, pageOpts{title: title, body: `<h1>` + esc(title) + `</h1><p>` +
		esc(t(c, "退订后不再收到这类邮件。", "You will stop getting this kind of mail.")) +
		`</p><form method="post"><input type="hidden" name="t" value="` + esc(c.Query("t")) + `"><button class="primary">` +
		esc(t(c, "退订", "Unsubscribe")) + `</button></form>`})
}

// unsubscribe is POST /unsubscribe, from the page's button or a mail
// client's RFC 8058 one-click request, which posts to the link itself.
func (s *Server) unsubscribe(c *gin.Context) {
	token := c.PostForm("t")
	if token == "" {
		token = c.Query("t")
	}
	payload, err := s.Keys.Verify(mailer.PurposeUnsubscribe, token)
	if err != nil {
		s.messagePage(c, http.StatusBadRequest, t(c, "链接无效", "Link not valid"), t(c, "这个退订链接无效。", "This unsubscribe link is not valid."))
		return
	}
	ctx := c.Request.Context()
	parts := strings.Split(payload, ":")
	switch {
	case len(parts) == 2 && parts[0] == mailer.UnsubReplies:
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		off := false
		if u, err := s.Store.User(ctx, id); err == nil {
			_, err = s.Store.UpdateProfile(ctx, u.ID, profileNotify(&off))
			if err != nil {
				s.storeError(c, err)
				return
			}
		}
	case len(parts) == 2 && parts[0] == mailer.UnsubGuest:
		h, err := hex.DecodeString(parts[1])
		if err == nil {
			err = s.Store.GuestOptOut(ctx, h)
		}
		if err != nil {
			s.storeError(c, err)
			return
		}
	case len(parts) == 3 && parts[0] == mailer.UnsubOwner:
		siteID, _ := strconv.ParseInt(parts[1], 10, 64)
		userID, _ := strconv.ParseInt(parts[2], 10, 64)
		if role, err := s.Store.Role(ctx, siteID, userID); err == nil && role == model.RoleOwner {
			if err := s.Store.SetOwnerNotify(ctx, siteID, model.NotifyOff); err != nil {
				s.storeError(c, err)
				return
			}
		}
	}
	s.messagePage(c, http.StatusOK, t(c, "已退订", "Unsubscribed"), t(c, "不会再收到这类邮件了。", "You will not get this kind of mail again."))
}

// mutePage is GET /mute.
func (s *Server) mutePage(c *gin.Context) {
	if _, _, err := mailer.ParseMute(s.Keys, c.Query("t")); err != nil {
		s.messagePage(c, http.StatusBadRequest, t(c, "链接无效", "Link not valid"), t(c, "这个链接无效。", "This link is not valid."))
		return
	}
	title := t(c, "关闭这个讨论页的通知", "Stop mail from this page")
	s.writePage(c, pageOpts{title: title, body: `<h1>` + esc(title) + `</h1><p>` +
		esc(t(c, "关闭后，这个讨论页里的新回复不再通知你。可以在读者中心恢复。", "New replies on this page will no longer notify you. You can undo it in the reader center.")) +
		`</p><form method="post"><input type="hidden" name="t" value="` + esc(c.Query("t")) + `"><button class="primary">` +
		esc(t(c, "关闭通知", "Stop mail")) + `</button></form>`})
}

// muteByMail is POST /mute.
func (s *Server) muteByMail(c *gin.Context) {
	userID, threadID, err := mailer.ParseMute(s.Keys, c.PostForm("t"))
	if err != nil {
		s.messagePage(c, http.StatusBadRequest, t(c, "链接无效", "Link not valid"), t(c, "这个链接无效。", "This link is not valid."))
		return
	}
	if err := s.Store.Mute(c.Request.Context(), userID, threadID); err != nil {
		s.storeError(c, err)
		return
	}
	s.messagePage(c, http.StatusOK, t(c, "已关闭", "Done"), t(c, "这个讨论页的新回复不再通知你。", "New replies on this page will no longer notify you."))
}
