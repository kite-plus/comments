package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

// guestGone answers the requests of guest commenting, which is gone
// (moderation.md section 3.2).
func (s *Server) guestGone(c *gin.Context) {
	s.fail(c, http.StatusGone, codeGuestCommentsGone)
}

// notifyUrgentReport tells the site's owners about an illegal or privacy
// report at once, whatever their setting (moderation.md section 6.2).
func (s *Server) notifyUrgentReport(c *gin.Context, site model.Site, reportID int64) {
	owners, err := s.Store.OwnerContacts(c.Request.Context(), site.ID)
	if err != nil {
		s.Log.Error("report mail", "error", err)
		return
	}
	for _, o := range owners {
		payload := encodeJSON(gin.H{"report_id": reportID, "site_id": site.ID, "user_id": o.UserID})
		if err := s.Store.EnqueueMail(c.Request.Context(), store.Mail{Kind: "report", ToEnc: o.EmailEnc, Locale: o.Locale, Payload: payload}); err != nil {
			s.Log.Error("report mail", "error", err)
		}
	}
}

// guestConfirmPage is GET /guest-email/confirm: it only shows a button.
// Links mailed before guest comments ended keep working until they expire.
func (s *Server) guestConfirmPage(c *gin.Context) {
	title := t(c, "确认接收回复通知", "Confirm reply notifications")
	s.writePage(c, pageOpts{title: title, body: `<h1>` + esc(title) + `</h1><p>` +
		esc(t(c, "确认后，有人回复你在博客上的访客评论时会通知这个邮箱。", "Once confirmed, this address hears when someone replies to your guest comments.")) +
		`</p><form method="post"><input type="hidden" name="t" value="` + esc(c.Query("t")) + `"><button class="primary">` +
		esc(t(c, "确认", "Confirm")) + `</button></form>`})
}

// guestConfirm is POST /guest-email/confirm.
func (s *Server) guestConfirm(c *gin.Context) {
	err := s.Store.ConfirmGuestConsent(c.Request.Context(), crypt.Hash(c.PostForm("t")))
	if errors.Is(err, store.ErrNotFound) {
		s.messagePage(c, http.StatusBadRequest, t(c, "链接无效", "Link not valid"),
			t(c, "这个确认链接无效或已过期。", "This confirmation link is not valid or has expired."))
		return
	}
	if err != nil {
		s.storeError(c, err)
		return
	}
	s.messagePage(c, http.StatusOK, t(c, "已确认", "Confirmed"), t(c, "有人回复你时，我们会发邮件通知你。", "We will mail you when someone replies."))
}
