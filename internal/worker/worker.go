// Package worker runs the service's background work: sending mail from the
// outbox, gathering owner notifications and the daily digest, cleaning up
// by the retention periods, reconciling identity service events, rendering
// old comments again, running imports and rechecking site origins. It meets
// the API only in the database.
package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/idp"
	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/markup"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
	"github.com/kite-plus/comments/internal/spam"
	"github.com/kite-plus/comments/internal/store"
	"github.com/kite-plus/comments/internal/verify"
)

// Mailer sends one message; *mailer.Sender is the real one.
type Mailer interface {
	Send(ctx context.Context, m mailer.Message) error
}

// Worker holds what the jobs need.
type Worker struct {
	Store     *store.Store
	Keys      *crypt.Keys
	Mailer    Mailer // nil leaves mail queued
	PublicURL string
	Issuer    string
	IDP       *idp.Client // nil skips reconciliation
	Fetcher   *verify.Fetcher
	Log       *slog.Logger
	Now       func() time.Time

	lastDigest   time.Time
	warnedNoMail time.Time
}

func (w *Worker) now() time.Time {
	if w.Now == nil {
		return time.Now()
	}
	return w.Now()
}

// Run loops until ctx ends.
func (w *Worker) Run(ctx context.Context) error {
	mail := time.NewTicker(5 * time.Second)
	minute := time.NewTicker(time.Minute)
	hour := time.NewTicker(time.Hour)
	defer mail.Stop()
	defer minute.Stop()
	defer hour.Stop()
	w.Hourly(ctx)
	w.Minutely(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-mail.C:
			w.SendMail(ctx)
		case <-minute.C:
			w.Minutely(ctx)
		case <-hour.C:
			w.Hourly(ctx)
		}
	}
}

// Minutely gathers owner mail, runs imports and renders stale comments.
func (w *Worker) Minutely(ctx context.Context) {
	w.queueOwners(ctx, model.NotifyInstant, "owner")
	now := w.now().UTC()
	if now.Hour() == policy.DigestHourUTC && now.Sub(w.lastDigest) > 2*time.Hour {
		w.lastDigest = now
		w.queueOwners(ctx, model.NotifyDaily, "digest")
	}
	w.runImports(ctx)
	w.rerender(ctx)
}

// Hourly cleans up, reconciles and rechecks.
func (w *Worker) Hourly(ctx context.Context) {
	if n, err := w.Store.Cleanup(ctx); err != nil {
		w.Log.Error("cleanup", "error", err)
	} else if len(n) > 0 {
		w.Log.Info("cleanup", "rows", n)
	}
	w.Reconcile(ctx)
	w.recheckOrigins(ctx)
}

func (w *Worker) queueOwners(ctx context.Context, mode, kind string) {
	due, err := w.Store.OwnersDue(ctx, mode)
	if err != nil {
		w.Log.Error("owner mail", "error", err)
		return
	}
	for _, o := range due {
		if _, err := w.Store.QueueOwnerMail(ctx, o, kind); err != nil {
			w.Log.Error("owner mail", "error", err)
		}
	}
}

// Reconcile fetches identity service events missed by the webhook
// (login.md section 7). Only the first page of a run starts before the
// stored cursor, by idp.SearchOverlap; later pages start at the last event
// seen, so a burst of events cannot hold a run on one page.
func (w *Worker) Reconcile(ctx context.Context) {
	if w.IDP == nil {
		return
	}
	stored, err := w.Store.IDPCursor(ctx)
	if err != nil {
		w.Log.Error("reconcile", "error", err)
		return
	}
	from := w.now().Add(-policy.IDPEventsKept)
	if t, err := time.Parse(time.RFC3339Nano, stored); err == nil {
		from = t.Add(-idp.SearchOverlap)
	}
	// A subject whose state cannot be read is retried next run: the stored
	// cursor never moves past the earliest such event.
	var retryFrom *time.Time
	var last time.Time
	for range 50 {
		events, pageLast, err := w.IDP.Search(ctx, from, reconcilePage)
		if err != nil {
			w.Log.Warn("reconcile", "error", err)
			return
		}
		applied := 0
		for _, ev := range events {
			ok, err := idp.Apply(ctx, w.Store, w.IDP, w.Issuer, ev)
			if err != nil {
				w.Log.Warn("reconcile: event left for the next run", "event", ev.ID, "error", err)
				if retryFrom == nil || ev.At.Before(*retryFrom) {
					at := ev.At
					retryFrom = &at
				}
				continue
			}
			if ok {
				applied++
			}
		}
		if applied > 0 {
			w.Log.Info("reconcile", "events", len(events), "applied", applied)
		}
		progressed := pageLast.After(last)
		last = pageLast
		cursor := last
		if retryFrom != nil && retryFrom.Before(cursor) {
			cursor = *retryFrom
		}
		if !cursor.IsZero() {
			if err := w.Store.SetIDPCursor(ctx, cursor.UTC().Format(time.RFC3339Nano)); err != nil {
				w.Log.Error("reconcile", "error", err)
				return
			}
		}
		if len(events) < reconcilePage || !progressed {
			return
		}
		// Events at exactly the last instant come again and deduplicate.
		from = last.Add(-time.Microsecond)
	}
}

// reconcilePage is the event search's page size.
const reconcilePage = 500

func (w *Worker) rerender(ctx context.Context) {
	stale, err := w.Store.StaleRenders(ctx, markup.Version, 200)
	if err != nil {
		w.Log.Error("rerender", "error", err)
		return
	}
	for _, c := range stale {
		if err := w.Store.SetRender(ctx, c.ID, markup.Render(c.BodyMD).HTML, markup.Version); err != nil {
			w.Log.Error("rerender", "error", err)
			return
		}
	}
}

func (w *Worker) recheckOrigins(ctx context.Context) {
	if w.Fetcher == nil {
		return
	}
	due, err := w.Store.OriginsDue(ctx, 50)
	if err != nil {
		w.Log.Error("recheck", "error", err)
		return
	}
	for _, d := range due {
		hashes, err := w.Store.VerificationHashes(ctx, d.SiteID)
		if err != nil {
			w.Log.Error("recheck", "error", err)
			return
		}
		if d.ProofHash != nil {
			hashes = append(hashes, d.ProofHash)
		}
		r := w.Fetcher.Check(ctx, d.Origin, func(tok string) bool {
			h := crypt.Hash(tok)
			for _, x := range hashes {
				if string(x) == string(h) {
					return true
				}
			}
			return false
		})
		first, dropped, err := w.Store.RecordRecheck(ctx, d.SiteID, d.Origin, r.OK)
		if err != nil {
			w.Log.Error("recheck", "error", err)
			continue
		}
		if first || dropped {
			w.Log.Info("origin recheck failed", "dropped", dropped)
			owners, err := w.Store.OwnerContacts(ctx, d.SiteID)
			if err == nil {
				for _, o := range owners {
					payload, _ := json.Marshal(map[string]any{"site_id": d.SiteID, "origin": d.Origin, "dropped": dropped, "detail": r.Detail})
					_ = w.Store.EnqueueMail(ctx, store.Mail{Kind: "recheck", ToEnc: o.EmailEnc, Locale: o.Locale, Payload: payload})
				}
			}
		}
	}
}

// SendMail sends due mail, one message per coalesce key.
func (w *Worker) SendMail(ctx context.Context) {
	if w.Mailer == nil {
		if w.now().Sub(w.warnedNoMail) > time.Hour {
			w.warnedNoMail = w.now()
			w.Log.Warn("COMMENTS_SMTP_URL is not set; mail stays queued")
		}
		return
	}
	for range 10 {
		batch, err := w.Store.ClaimMail(ctx, 20, 5*time.Minute)
		if err != nil {
			w.Log.Error("mail", "error", err)
			return
		}
		if len(batch) == 0 {
			return
		}
		groups := map[string][]store.Mail{}
		var order []string
		for _, m := range batch {
			key := m.CoalesceKey
			if key == "" {
				key = "id:" + strconv.FormatInt(m.ID, 10)
			}
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], m)
		}
		for _, key := range order {
			w.sendGroup(ctx, groups[key])
		}
	}
}

func (w *Worker) sendGroup(ctx context.Context, group []store.Mail) {
	ids := make([]int64, len(group))
	for i, m := range group {
		ids[i] = m.ID
	}
	msg, after, err := w.render(ctx, group)
	switch {
	case errors.Is(err, errNothing):
		if err := w.Store.MailDropped(ctx, ids); err != nil {
			w.Log.Error("mail", "error", err)
		}
		return
	case err != nil:
		w.Log.Error("mail render", "kind", group[0].Kind, "error", scrub(err))
		if err := w.Store.MailFailed(ctx, ids, scrub(err)); err != nil {
			w.Log.Error("mail", "error", err)
		}
		return
	}
	if err := w.Mailer.Send(ctx, msg); err != nil {
		w.Log.Warn("mail send", "kind", group[0].Kind, "error", scrub(err))
		if err := w.Store.MailFailed(ctx, ids, scrub(err)); err != nil {
			w.Log.Error("mail", "error", err)
		}
		return
	}
	if err := w.Store.MailSent(ctx, ids); err != nil {
		w.Log.Error("mail", "error", err)
	}
	if after != nil {
		after()
	}
}

var errNothing = errors.New("nothing left to send")

var address = regexp.MustCompile(`[^\s<>"'(),;:]+@[^\s<>"'(),;:]+`)

// scrub keeps recipients out of logs and outbox.last_error: an SMTP error
// becomes its reply code, anything else loses every address in it.
func scrub(err error) string {
	if tp, ok := errors.AsType[*textproto.Error](err); ok {
		return fmt.Sprintf("smtp %d", tp.Code)
	}
	msg := address.ReplaceAllString(err.Error(), "<address>")
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200])
	}
	return msg
}

// userRecipient re-reads an account when its mail goes out: deleted,
// suspended or opted-out accounts get nothing, and the address is the
// account's current one, not the one queued.
func (w *Worker) userRecipient(ctx context.Context, userID int64, needReplies bool) (model.User, string, error) {
	u, err := w.Store.User(ctx, userID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (u.Suspended() || u.EmailEnc == nil || (needReplies && !u.NotifyReplies))) {
		return u, "", errNothing
	}
	if err != nil {
		return u, "", err
	}
	to, err := w.Keys.Decrypt(u.EmailEnc)
	return u, to, err
}

type payload struct {
	CommentID  int64   `json:"comment_id"`
	CommentIDs []int64 `json:"comment_ids"`
	UserID     int64   `json:"user_id"`
	SiteID     int64   `json:"site_id"`
	ReportID   int64   `json:"report_id"`
	ClaimID    int64   `json:"claim_id"`
	Token      string  `json:"token"`
	Origin     string  `json:"origin"`
	Dropped    bool    `json:"dropped"`
	Detail     string  `json:"detail"`
}

// render builds the message for a group of mail sharing a coalesce key;
// after, if set, runs once it is sent.
func (w *Worker) render(ctx context.Context, group []store.Mail) (mailer.Message, func(), error) {
	first := group[0]
	to, err := w.Keys.Decrypt(first.ToEnc)
	if err != nil {
		return mailer.Message{}, nil, fmt.Errorf("recipient: %w", err)
	}
	var ps []payload
	for _, m := range group {
		var p payload
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			return mailer.Message{}, nil, err
		}
		ps = append(ps, p)
	}
	locale := first.Locale
	base := w.PublicURL
	switch first.Kind {
	case "reply", "guest_reply":
		var ids []int64
		for _, p := range ps {
			ids = append(ids, p.CommentID)
		}
		muted := map[int64]bool{}
		if first.Kind == "reply" {
			u, current, err := w.userRecipient(ctx, ps[0].UserID, true)
			if err != nil {
				return mailer.Message{}, nil, err
			}
			to, locale = current, u.Locale
			list, err := w.Store.Muted(ctx, u.ID)
			if err != nil {
				return mailer.Message{}, nil, err
			}
			for _, id := range list {
				muted[id] = true
			}
		} else if ok, err := w.Store.GuestMailable(ctx, w.Keys.EmailHMAC(to)); err != nil || !ok {
			if err != nil {
				return mailer.Message{}, nil, err
			}
			return mailer.Message{}, nil, errNothing
		}
		all, allThreads, err := w.items(ctx, ids, locale, true)
		if err != nil {
			return mailer.Message{}, nil, err
		}
		var items []mailer.Item
		var threads []int64
		for i, it := range all {
			if !muted[allThreads[i]] {
				items, threads = append(items, it), append(threads, allThreads[i])
			}
		}
		if len(items) == 0 {
			return mailer.Message{}, nil, errNothing
		}
		var unsub string
		var after func()
		if first.Kind == "reply" {
			userID := ps[0].UserID
			for i := range items {
				items[i].MuteURL = mailer.MuteURL(base, w.Keys, userID, threads[i])
			}
			unsub = mailer.UnsubscribeURL(base, w.Keys, mailer.UnsubReplies+":"+strconv.FormatInt(userID, 10))
			after = func() {
				if err := w.Store.NotificationsEmailed(ctx, userID, ids); err != nil {
					w.Log.Error("mail", "error", err)
				}
			}
		} else {
			unsub = mailer.UnsubscribeURL(base, w.Keys, mailer.UnsubGuest+":"+hex.EncodeToString(w.Keys.EmailHMAC(to)))
		}
		return mailer.Replies(locale, to, items, unsub), after, nil
	case "owner", "digest":
		p := ps[0]
		site, err := w.Store.SiteByID(ctx, p.SiteID)
		if err != nil || site.OwnerNotify == model.NotifyOff {
			return mailer.Message{}, nil, errNothing
		}
		if to, err = w.ownerRecipient(ctx, site.ID, p.UserID); err != nil {
			return mailer.Message{}, nil, err
		}
		shown := p.CommentIDs
		if len(shown) > 20 {
			shown = shown[:20]
		}
		items, _, err := w.items(ctx, shown, locale, false)
		if err != nil {
			return mailer.Message{}, nil, err
		}
		if len(items) == 0 {
			return mailer.Message{}, nil, errNothing
		}
		unsub := mailer.UnsubscribeURL(base, w.Keys, fmt.Sprintf("%s:%d:%d", mailer.UnsubOwner, site.ID, p.UserID))
		return mailer.Owner(locale, to, site.Name, items, len(p.CommentIDs), base+"/console/sites/"+site.PublicID+"/comments", unsub,
			first.Kind == "digest"), nil, nil
	case "report":
		p := ps[0]
		site, err := w.Store.SiteByID(ctx, p.SiteID)
		if err != nil {
			return mailer.Message{}, nil, errNothing
		}
		if to, err = w.ownerRecipient(ctx, site.ID, p.UserID); err != nil {
			return mailer.Message{}, nil, err
		}
		_, commentID, err := w.Store.ReportSite(ctx, p.ReportID)
		if err != nil {
			return mailer.Message{}, nil, errNothing
		}
		items, _, err := w.items(ctx, []int64{commentID}, locale, false)
		if err != nil || len(items) == 0 {
			return mailer.Message{}, nil, errNothing
		}
		reason, err := w.Store.ReportReason(ctx, p.ReportID)
		if err != nil {
			return mailer.Message{}, nil, errNothing
		}
		return mailer.Report(locale, to, site.Name, reason, items[0], base+"/console/sites/"+site.PublicID+"/reports"), nil, nil
	case "claim":
		p := ps[0]
		return mailer.Claim(locale, to, fmt.Sprintf("%s/me/claims/%d?t=%s", base, p.ClaimID, p.Token)), nil, nil
	case "guest_confirm":
		p := ps[0]
		name := ""
		if site, err := w.Store.SiteByID(ctx, p.SiteID); err == nil {
			name = site.Name
		}
		unsub := mailer.UnsubscribeURL(base, w.Keys, mailer.UnsubGuest+":"+hex.EncodeToString(w.Keys.EmailHMAC(to)))
		return mailer.GuestConfirm(locale, to, name, base+"/guest-email/confirm?t="+p.Token, unsub), nil, nil
	case "recheck":
		p := ps[0]
		site, err := w.Store.SiteByID(ctx, p.SiteID)
		if err != nil {
			return mailer.Message{}, nil, errNothing
		}
		return mailer.Recheck(locale, to, site.Name, p.Origin, p.Detail, p.Dropped, base+"/console/sites/"+site.PublicID), nil, nil
	}
	return mailer.Message{}, nil, fmt.Errorf("unknown mail kind %q", first.Kind)
}

// items describes comments for mail; with publishedOnly, comments no
// longer published are left out. It also returns each item's thread.
func (w *Worker) items(ctx context.Context, ids []int64, locale string, publishedOnly bool) ([]mailer.Item, []int64, error) {
	list, err := w.Store.CommentsByID(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	var items []mailer.Item
	var threads []int64
	for _, c := range list {
		if publishedOnly && c.Status != model.StatusPublished {
			continue
		}
		if !publishedOnly && c.Status != model.StatusPublished && c.Status != model.StatusPending {
			continue
		}
		name := c.AuthorName
		// The first commenter chose the title: cleaned and cut before it
		// reaches anyone else's mail (sites.md section 4).
		c.ThreadTitle = cut(spam.CleanName(c.ThreadTitle), policy.MailTitleRunes)
		if c.AuthorKind == model.AuthorFormerUser || name == "" {
			name = map[bool]string{true: "已注销用户", false: "Deleted user"}[locale != "en"]
		}
		base, _, _ := strings.Cut(c.ThreadURL, "#")
		items = append(items, mailer.Item{SiteName: c.SiteName, ThreadTitle: c.ThreadTitle,
			Permalink: base + "#kc-" + strconv.FormatInt(c.ID, 10), Author: name,
			Excerpt: markup.Excerpt(c.BodyMD, policy.ExcerptRunes), Pending: c.Status == model.StatusPending})
		threads = append(threads, c.ThreadID)
	}
	return items, threads, nil
}

// ownerRecipient is userRecipient for a site's owner, who must still own
// the site.
func (w *Worker) ownerRecipient(ctx context.Context, siteID, userID int64) (string, error) {
	role, err := w.Store.Role(ctx, siteID, userID)
	if err != nil {
		return "", err
	}
	if role != model.RoleOwner {
		return "", errNothing
	}
	_, to, err := w.userRecipient(ctx, userID, false)
	return to, err
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
