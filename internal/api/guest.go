package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/pow"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
)

type powIn = pow.Solution

func (s *Server) powMAC() pow.MAC {
	return func(data []byte) []byte { return s.Keys.MAC("pow", data) }
}

// powChallenge is GET /api/v1/pow/challenge: the more an address posted as
// a guest lately, the larger the range to search (moderation.md 3.2).
func (s *Server) powChallenge(c *gin.Context) {
	site, ok := s.activeSite(c, c.Query("site"))
	if !ok {
		return
	}
	if site.GuestPolicy == model.GuestOff {
		s.fail(c, http.StatusForbidden, codeGuestNotAllowed)
		return
	}
	maxNumber := policy.PowBaseNumber
	for range s.guestPosts.count(c.ClientIP()) / policy.PowStepPosts {
		maxNumber = min(maxNumber*2, policy.PowMaxNumber)
	}
	private(c, http.StatusOK, pow.New(s.powMAC(), site.PublicID, maxNumber, policy.PowTTL, s.Now()))
}

// guestAuthor checks a guest comment through rules 2 to 4 of
// moderation.md section 2 and fills in its author.
func (s *Server) guestAuthor(c *gin.Context, site model.Site, in commentIn, n *store.NewComment, newThread bool, ipKeys [][]byte) bool {
	g := in.Guest
	if g.Honeypot != "" {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return false
	}
	name := spam.CleanName(g.Name)
	email := validEmail(g.Email)
	homepage, okURL := validHomepage(g.URL)
	if name == "" || len([]rune(name)) > policy.DisplayNameRunes || email == "" || !okURL {
		s.fail(c, http.StatusBadRequest, codeInvalidRequest)
		return false
	}
	emailHMAC := s.Keys.EmailHMAC(email)
	banned, err := s.Store.Banned(c.Request.Context(), site.ID, nil, emailHMAC, ipKeys)
	if err != nil {
		s.storeError(c, err)
		return false
	}
	if banned {
		s.fail(c, http.StatusForbidden, codeForbidden)
		return false
	}
	if site.GuestPolicy == model.GuestOff {
		s.fail(c, http.StatusForbidden, codeGuestNotAllowed)
		return false
	}
	if in.Pow == nil {
		s.fail(c, http.StatusBadRequest, codePowInvalid)
		return false
	}
	expires, err := pow.Verify(s.powMAC(), *in.Pow, site.PublicID, s.Now())
	if err != nil {
		s.fail(c, http.StatusBadRequest, codePowInvalid)
		return false
	}
	fresh, err := s.Store.SpendPow(c.Request.Context(), pow.ID(in.Pow.Challenge), expires)
	if err != nil {
		s.storeError(c, err)
		return false
	}
	if !fresh {
		s.fail(c, http.StatusBadRequest, codePowInvalid)
		return false
	}
	ip := c.ClientIP()
	if newThread {
		if ok, retry := s.guestThreads.allow(ip); !ok {
			s.tooMany(c, retry)
			return false
		}
	}
	if ok, retry := s.guestPosts.allow(ip); !ok {
		s.tooMany(c, retry)
		return false
	}
	n.AuthorKind, n.AuthorName, n.AuthorURL = model.AuthorGuest, name, homepage
	n.AuthorEmailEnc, n.AuthorEmailHMAC = s.Keys.Encrypt(email), emailHMAC
	if g.Notify {
		// Reply mail only where this browser confirmed it before; anything
		// else asks again by mail once the comment exists.
		ok, err := s.Store.GuestConsent(c.Request.Context(), emailHMAC, browserHash(g.Token))
		if err != nil {
			s.storeError(c, err)
			return false
		}
		n.NotifyEmail = ok
	}
	return true
}

// browserHash is the stored form of the widget's guest token, nil when the
// browser sent none.
func browserHash(token string) []byte {
	if len(token) != 43 {
		return nil
	}
	return crypt.Hash(token)
}

// requestGuestConsent mails the address a confirmation link that turns on
// reply mail for this comment and this browser (moderation.md section
// 6.1). An address gets at most one such mail a day.
func (s *Server) requestGuestConsent(c *gin.Context, site model.Site, in commentIn, commentID int64) {
	email := validEmail(in.Guest.Email)
	locale := model.LocaleZH
	if in.Lang == model.LocaleEN {
		locale = model.LocaleEN
	}
	token := crypt.Token()
	enc := s.Keys.Encrypt(email)
	send, err := s.Store.RequestGuestConsent(c.Request.Context(), s.Keys.EmailHMAC(email), enc, browserHash(in.Guest.Token),
		commentID, crypt.Hash(token), locale)
	if err != nil {
		s.Log.Error("guest consent", "error", err)
		return
	}
	if !send {
		return
	}
	payload := encodeJSON(gin.H{"token": token, "site_id": site.ID})
	if err := s.Store.EnqueueMail(c.Request.Context(), store.Mail{Kind: "guest_confirm", ToEnc: enc, Locale: locale, Payload: payload}); err != nil {
		s.Log.Error("guest consent", "error", err)
	}
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
