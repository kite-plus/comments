package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

// ReportIn is a reader's report of a comment.
type ReportIn struct {
	SiteID     int64
	CommentID  int64
	ReporterID int64
	Reason     string
	Note       string
	// ReporterTrusted counts the report toward the automatic return to
	// review (moderation.md section 4.3).
	ReporterTrusted bool
}

// ReportResult says what a report led to.
type ReportResult struct {
	ID int64
	// Returned is set when the report sent the comment back to review.
	Returned bool
	// Duplicate is set when the reporter had an open report already.
	Duplicate bool
}

// CreateReport files a report and returns the comment to review once
// enough trusted accounts reported it.
func (s *Store) CreateReport(ctx context.Context, in ReportIn) (ReportResult, error) {
	var res ReportResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var status string
		var threadID int64
		err := tx.QueryRow(ctx, `SELECT status, thread_id FROM comments WHERE id = $1 AND site_id = $2 FOR UPDATE`,
			in.CommentID, in.SiteID).Scan(&status, &threadID)
		if err != nil {
			return notFound(err)
		}
		err = tx.QueryRow(ctx, `SELECT id FROM reports WHERE comment_id = $1 AND reporter_user_id = $2 AND status = 'open'`,
			in.CommentID, in.ReporterID).Scan(&res.ID)
		if err == nil {
			res.Duplicate = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO reports (comment_id, site_id, reporter_user_id, reason, note) VALUES ($1, $2, $3, $4, $5)
			RETURNING id`, in.CommentID, in.SiteID, in.ReporterID, in.Reason, in.Note).Scan(&res.ID); err != nil {
			return err
		}
		if !in.ReporterTrusted || status != model.StatusPublished {
			return nil
		}
		var trusted int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT DISTINCT reporter_user_id AS uid FROM reports
			WHERE comment_id = $1 AND status = 'open' AND reporter_user_id IS NOT NULL) r
			WHERE `+trustedCond("r.uid", 2), in.CommentID, seconds(policy.TrustedAccountAge), policy.TrustedComments,
			policy.TrustedSites).Scan(&trusted); err != nil {
			return err
		}
		if trusted < policy.ReportThreshold {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET status = 'pending', pinned_at = NULL, updated_at = now() WHERE id = $1`, in.CommentID); err != nil {
			return err
		}
		if err := recountThreads(ctx, tx, []int64{threadID}); err != nil {
			return err
		}
		res.Returned = true
		return addEvent(ctx, tx, in.SiteID, &in.CommentID, Event{Action: "report_threshold", Detail: map[string]any{"report_id": res.ID}})
	})
	return res, err
}

// ReportInfo is a report with its comment, for the console.
type ReportInfo struct {
	ID         int64
	CommentID  int64
	SiteID     string
	SiteName   string
	Reason     string
	Note       string
	Status     string
	CreatedAt  time.Time
	ResolvedAt *time.Time
	Comment    model.Comment
}

// Reports lists a site's reports, or the platform's queue of illegal and
// privacy reports when siteID is 0.
func (s *Store) Reports(ctx context.Context, siteID int64, status string, cur *model.Cursor, limit int) ([]ReportInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.comment_id, st.public_id::text, st.name, r.reason, r.note, r.status, r.created_at, r.resolved_at
		FROM reports r JOIN sites st ON st.id = r.site_id
		WHERE (($1 <> 0 AND r.site_id = $1) OR ($1 = 0 AND r.reason IN ('illegal', 'privacy')))
		AND ($2 = '' OR r.status = $2)
		AND ($3::timestamptz IS NULL OR (r.created_at, r.id) < ($3, $4))
		ORDER BY r.created_at DESC, r.id DESC LIMIT $5`, siteID, status, cursorAt(cur), cursorID(cur), limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportInfo, error) {
		var i ReportInfo
		err := r.Scan(&i.ID, &i.CommentID, &i.SiteID, &i.SiteName, &i.Reason, &i.Note, &i.Status, &i.CreatedAt, &i.ResolvedAt)
		return i, err
	})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Comment, err = s.Comment(ctx, list[i].CommentID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// ResolveReport marks a report resolved or dismissed. siteID 0 is the
// platform, which may resolve any illegal or privacy report.
func (s *Store) ResolveReport(ctx context.Context, siteID, reportID int64, status string, by int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE reports SET status = $3, resolved_at = now(), resolved_by = $4
		WHERE id = $2 AND (($1 <> 0 AND site_id = $1) OR ($1 = 0 AND reason IN ('illegal', 'privacy')))`, siteID, reportID, status, by)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ReportSite returns the site of a report, for the platform queue.
func (s *Store) ReportSite(ctx context.Context, reportID int64) (siteID, commentID int64, err error) {
	err = s.pool.QueryRow(ctx, `SELECT site_id, comment_id FROM reports WHERE id = $1`, reportID).Scan(&siteID, &commentID)
	return siteID, commentID, notFound(err)
}

// Ban is a bans row. A reader is banned by identity (issuer, subject), so
// the ban outlives the account row and meets the reader again after they
// delete their data and sign in anew (data-model.md section 3.5).
type Ban struct {
	ID        int64
	SiteID    *int64
	Issuer    string
	Subject   string
	AuthorID  string // the banned reader's site author ID, while the account exists
	Name      string // the banned reader's display name, while the account exists
	EmailHMAC []byte
	IPHMAC    []byte
	Reason    string
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// CreateBan adds a ban; siteID nil is a platform-wide ban.
func (s *Store) CreateBan(ctx context.Context, b Ban, by int64) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO bans (site_id, issuer, subject, email_hmac, ip_hmac, reason, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, b.SiteID, nullString(b.Issuer), nullString(b.Subject),
			b.EmailHMAC, b.IPHMAC, b.Reason, by, b.ExpiresAt).Scan(&id); err != nil {
			return err
		}
		site := int64(0)
		action := "platform_ban"
		if b.SiteID != nil {
			site, action = *b.SiteID, "ban"
		}
		detail := map[string]any{"ban_id": id}
		if b.ExpiresAt != nil {
			detail["expires_at"] = b.ExpiresAt.UTC()
		}
		return addEvent(ctx, tx, site, nil, Event{Action: action, Actor: &by, Detail: detail})
	})
	return id, err
}

// Bans lists the live bans of a site, or platform bans when siteID is 0.
func (s *Store) Bans(ctx context.Context, siteID int64) ([]Ban, error) {
	rows, err := s.pool.Query(ctx, `SELECT b.id, b.site_id, coalesce(b.issuer, ''), coalesce(b.subject, ''),
		coalesce(sa.public_id::text, ''), coalesce(u.display_name, ''),
		b.email_hmac, b.ip_hmac, b.reason, b.created_at, b.expires_at
		FROM bans b LEFT JOIN user_identities ui ON ui.issuer = b.issuer AND ui.subject = b.subject
		LEFT JOIN users u ON u.id = ui.user_id
		LEFT JOIN site_authors sa ON sa.user_id = u.id AND sa.site_id = b.site_id
		WHERE (($1 <> 0 AND b.site_id = $1) OR ($1 = 0 AND b.site_id IS NULL))
		AND (b.expires_at IS NULL OR b.expires_at > now()) ORDER BY b.created_at DESC`, siteID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ban, error) {
		var b Ban
		err := r.Scan(&b.ID, &b.SiteID, &b.Issuer, &b.Subject, &b.AuthorID, &b.Name, &b.EmailHMAC, &b.IPHMAC, &b.Reason, &b.CreatedAt, &b.ExpiresAt)
		return b, err
	})
}

// DeleteBan lifts a ban of a site (siteID 0 for platform bans).
func (s *Store) DeleteBan(ctx context.Context, siteID, banID, by int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM bans WHERE id = $2 AND (($1 <> 0 AND site_id = $1) OR ($1 = 0 AND site_id IS NULL))`, siteID, banID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return addEvent(ctx, tx, siteID, nil, Event{Action: "unban", Actor: &by, Detail: map[string]any{"ban_id": banID}})
	})
}

// UserByAuthorID resolves a site author ID to the account behind it.
func (s *Store) UserByAuthorID(ctx context.Context, siteID int64, authorID string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM site_authors WHERE site_id = $1 AND public_id::text = $2`, siteID, authorID).Scan(&id)
	return id, notFound(err)
}

// EventInfo is a moderation_events row for the console.
type EventInfo struct {
	ID        int64
	CommentID *int64
	ActorName string
	Action    string
	Detail    map[string]any
	CreatedAt time.Time
}

// Events lists a site's moderation record.
func (s *Store) Events(ctx context.Context, siteID int64, cur *model.Cursor, limit int) ([]EventInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.comment_id, coalesce(u.display_name, ''), e.action, e.detail, e.created_at
		FROM moderation_events e LEFT JOIN users u ON u.id = e.actor_user_id
		WHERE e.site_id = $1 AND ($2::timestamptz IS NULL OR (e.created_at, e.id) < ($2, $3))
		ORDER BY e.created_at DESC, e.id DESC LIMIT $4`, siteID, cursorAt(cur), cursorID(cur), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EventInfo, error) {
		var e EventInfo
		var raw []byte
		if err := r.Scan(&e.ID, &e.CommentID, &e.ActorName, &e.Action, &raw, &e.CreatedAt); err != nil {
			return e, err
		}
		err := json.Unmarshal(raw, &e.Detail)
		return e, err
	})
}

// Event returns one moderation event of a site.
func (s *Store) Event(ctx context.Context, siteID, id int64) (EventInfo, error) {
	var e EventInfo
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id, comment_id, '', action, detail, created_at FROM moderation_events WHERE site_id = $1 AND id = $2`,
		siteID, id).Scan(&e.ID, &e.CommentID, &e.ActorName, &e.Action, &raw, &e.CreatedAt)
	if err != nil {
		return e, notFound(err)
	}
	return e, json.Unmarshal(raw, &e.Detail)
}

// QueueQuery selects comments for the console's moderation queue.
type QueueQuery struct {
	SiteID   int64
	Status   string // "" for all but deleted; "reported" for open reports
	ThreadID int64
	Cursor   *model.Cursor
	Limit    int
}

// Queue lists a site's comments for moderation, newest first, with their
// threads.
func (s *Store) Queue(ctx context.Context, q QueueQuery) ([]model.Comment, *model.Cursor, error) {
	rows, err := s.pool.Query(ctx, withPlace(commentSelect+`
		WHERE c.site_id = $1
		AND CASE $2 WHEN '' THEN c.status <> 'deleted'
			WHEN 'reported' THEN EXISTS (SELECT 1 FROM reports r WHERE r.comment_id = c.id AND r.status = 'open')
			ELSE c.status = $2 END
		AND ($3 = 0 OR c.thread_id = $3)
		AND ($4::timestamptz IS NULL OR (c.created_at, c.id) < ($4, $5))
		ORDER BY c.created_at DESC, c.id DESC LIMIT $6`)+` ORDER BY x.created_at DESC, x.id DESC`,
		q.SiteID, q.Status, q.ThreadID, cursorAt(q.Cursor), cursorID(q.Cursor), q.Limit+1)
	list, err := collectPlaced(rows, err, nil)
	if err != nil {
		return nil, nil, err
	}
	var next *model.Cursor
	if len(list) > q.Limit {
		list = list[:q.Limit]
		last := list[len(list)-1]
		next = &model.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	return list, next, nil
}

// ThreadsOf lists a site's threads, most recently commented first.
func (s *Store) ThreadsOf(ctx context.Context, siteID int64, cur *model.Cursor, limit int) ([]model.Thread, *model.Cursor, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+threadColumns+` FROM threads
		WHERE site_id = $1 AND ($2::timestamptz IS NULL OR (coalesce(last_comment_at, created_at), id) < ($2, $3))
		ORDER BY coalesce(last_comment_at, created_at) DESC, id DESC LIMIT $4`, siteID, cursorAt(cur), cursorID(cur), limit+1)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Thread, error) { return scanThread(r) })
	if err != nil {
		return nil, nil, err
	}
	var next *model.Cursor
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		at := last.CreatedAt
		if last.LastCommentAt != nil {
			at = *last.LastCommentAt
		}
		next = &model.Cursor{At: at, ID: last.ID}
	}
	return list, next, nil
}

// ThreadUpdate changes the fields that are set.
type ThreadUpdate struct {
	Title         *string
	URL           *string
	PagePublished **time.Time
	Locked        *bool
}

// UpdateThread is a manager editing a thread.
func (s *Store) UpdateThread(ctx context.Context, siteID, threadID int64, u ThreadUpdate, actor int64) (model.Thread, error) {
	setPublished := u.PagePublished != nil
	var published *time.Time
	if setPublished {
		published = *u.PagePublished
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var wasLocked bool
		if err := tx.QueryRow(ctx, `SELECT locked_at IS NOT NULL FROM threads WHERE site_id = $1 AND id = $2 FOR UPDATE`,
			siteID, threadID).Scan(&wasLocked); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE threads SET title = coalesce($3, title), url = coalesce($4, url),
			page_published_at = CASE WHEN $5 THEN $6 ELSE page_published_at END,
			locked_at = CASE WHEN $7::boolean IS NULL THEN locked_at WHEN $7 THEN coalesce(locked_at, now()) END,
			updated_at = now() WHERE site_id = $1 AND id = $2`, siteID, threadID, u.Title, u.URL, setPublished, published, u.Locked); err != nil {
			return err
		}
		if u.Locked != nil && *u.Locked != wasLocked {
			action := "unlock"
			if *u.Locked {
				action = "lock"
			}
			return addEvent(ctx, tx, siteID, nil, Event{Action: action, Actor: &actor, Detail: map[string]any{"thread_id": threadID}})
		}
		return nil
	})
	if err != nil {
		return model.Thread{}, err
	}
	return s.Thread(ctx, siteID, threadID)
}

// CommentIPHash returns the IP hash a comment was stored with, for an IP
// ban; nil once the retention period cleared it.
func (s *Store) CommentIPHash(ctx context.Context, id int64) ([]byte, error) {
	var h []byte
	err := s.pool.QueryRow(ctx, `SELECT ip_hmac FROM comments WHERE id = $1`, id).Scan(&h)
	return h, notFound(err)
}

// ReportReason returns a report's reason.
func (s *Store) ReportReason(ctx context.Context, reportID int64) (string, error) {
	var r string
	err := s.pool.QueryRow(ctx, `SELECT reason FROM reports WHERE id = $1`, reportID).Scan(&r)
	return r, notFound(err)
}

// Identity returns the (issuer, subject) of an account.
func (s *Store) Identity(ctx context.Context, userID int64) (issuer, subject string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT issuer, subject FROM user_identities WHERE user_id = $1 ORDER BY linked_at LIMIT 1`,
		userID).Scan(&issuer, &subject)
	return issuer, subject, notFound(err)
}
