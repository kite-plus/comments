package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

var (
	ErrThreadLocked = errors.New("thread is locked")
	ErrBadParent    = errors.New("parent is not a published comment of this thread")
)

const commentCols = `c.id, c.site_id, c.thread_id, c.root_id, c.parent_id, c.author_kind, c.author_user_id,
	CASE WHEN c.author_kind = 'user' THEN u.display_name ELSE coalesce(c.author_name, '') END AS author_display,
	CASE WHEN c.author_kind = 'user' THEN coalesce(u.homepage_url, '') ELSE coalesce(c.author_url, '') END AS author_link,
	coalesce(c.author_ref, '') AS author_ref_text, coalesce(sa.public_id::text, '') AS author_public,
	coalesce(u.avatar_kind = 'idp' AND a.user_id IS NOT NULL, false) AS author_has_avatar, coalesce(a.etag, '') AS author_avatar_tag,
	c.author_email_enc, c.author_email_hmac, c.notify_email,
	c.body_md, c.body_html, c.render_version, c.status, c.pinned_at, c.source, coalesce(c.source_id, '') AS source_ref,
	c.created_at, c.updated_at, c.edited_at, c.published_at, coalesce(m.role, '') AS author_role,
	p.id AS reply_to_id, CASE WHEN p.author_kind = 'user' THEN pu.display_name ELSE coalesce(p.author_name, '') END AS reply_to_name,
	p.author_kind AS reply_to_kind`

const commentFrom = ` FROM comments c
	LEFT JOIN users u ON u.id = c.author_user_id
	LEFT JOIN site_authors sa ON sa.site_id = c.site_id AND sa.user_id = c.author_user_id
	LEFT JOIN avatars a ON a.user_id = u.id
	LEFT JOIN site_members m ON m.site_id = c.site_id AND m.user_id = c.author_user_id
	LEFT JOIN comments p ON p.id = c.parent_id AND c.parent_id <> c.root_id
	LEFT JOIN users pu ON pu.id = p.author_user_id`

// commentSelect resolves a comment's author for display: a reader's current
// name, homepage and avatar, their site author ID and their role.
const commentSelect = `SELECT ` + commentCols + commentFrom

func scanComment(row pgx.Row, extra ...any) (model.Comment, error) {
	var c model.Comment
	var replyID *int64
	var replyName, replyKind *string
	dest := []any{&c.ID, &c.SiteID, &c.ThreadID, &c.RootID, &c.ParentID, &c.AuthorKind, &c.AuthorUserID,
		&c.AuthorName, &c.AuthorURL, &c.AuthorRef, &c.AuthorPublicID, &c.AuthorHasAvatar, &c.AuthorAvatarTag,
		&c.AuthorEmailEnc, &c.AuthorEmailHMAC, &c.NotifyEmail,
		&c.BodyMD, &c.BodyHTML, &c.RenderVersion, &c.Status, &c.PinnedAt, &c.Source, &c.SourceID,
		&c.CreatedAt, &c.UpdatedAt, &c.EditedAt, &c.PublishedAt, &c.Badge,
		&replyID, &replyName, &replyKind}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return c, notFound(err)
	}
	if replyID != nil {
		c.ReplyTo = &model.ReplyTo{ID: *replyID, Name: deref(replyName)}
	}
	return c, nil
}

func collectComments(rows pgx.Rows, err error) ([]model.Comment, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Comment, error) { return scanComment(r) })
}

// Comment returns one comment.
func (s *Store) Comment(ctx context.Context, id int64) (model.Comment, error) {
	return scanComment(s.pool.QueryRow(ctx, commentSelect+` WHERE c.id = $1`, id))
}

// ThreadByKey returns the thread of a page, or ErrNotFound.
func (s *Store) ThreadByKey(ctx context.Context, siteID int64, key string) (model.Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, `SELECT `+threadColumns+` FROM threads WHERE site_id = $1 AND page_key = $2`, siteID, key))
}

// Thread returns a thread by ID within a site.
func (s *Store) Thread(ctx context.Context, siteID, id int64) (model.Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, `SELECT `+threadColumns+` FROM threads WHERE site_id = $1 AND id = $2`, siteID, id))
}

const threadColumns = `id, site_id, page_key, url, title, page_published_at, locked_at, comment_count, last_comment_at, created_at, updated_at`

func scanThread(row pgx.Row) (model.Thread, error) {
	var t model.Thread
	err := row.Scan(&t.ID, &t.SiteID, &t.PageKey, &t.URL, &t.Title, &t.PagePublishedAt, &t.LockedAt, &t.CommentCount,
		&t.LastCommentAt, &t.CreatedAt, &t.UpdatedAt)
	return t, notFound(err)
}

// ListQuery selects top-level comments of a thread.
type ListQuery struct {
	ThreadID int64
	Newest   bool
	Cursor   *model.Cursor
	Limit    int
	// Statuses to show; empty means published only. Top-level comments in
	// other states that have visible replies come back as placeholders.
	Statuses []string
	// Around asks for the page holding this comment.
	Around int64
}

// ListPage is a page of top-level comments with their first replies.
type ListPage struct {
	Comments []model.Comment
	Next     *model.Cursor
	// Partial is set when an around page does not start at the beginning.
	Partial bool
}

// ListComments returns a page of top-level comments, pinned ones first on
// the first page, each with its first replies and reply count.
func (s *Store) ListComments(ctx context.Context, q ListQuery) (ListPage, error) {
	statuses := q.Statuses
	if len(statuses) == 0 {
		statuses = []string{model.StatusPublished}
	}
	limit := q.Limit
	repliesUpTo := int64(0)
	var targetRoot int64
	if q.Around != 0 {
		var root int64
		var parent *int64
		var status string
		err := s.pool.QueryRow(ctx, `SELECT coalesce(root_id, id), parent_id, status FROM comments WHERE id = $1 AND thread_id = $2`,
			q.Around, q.ThreadID).Scan(&root, &parent, &status)
		if err == nil && contains(statuses, status) {
			targetRoot = root
			var pinned bool
			var before int
			err = s.pool.QueryRow(ctx, `SELECT r.pinned_at IS NOT NULL,
				(SELECT count(*) FROM comments o WHERE o.thread_id = r.thread_id AND o.root_id IS NULL AND o.pinned_at IS NULL
				  AND o.status = ANY($3) AND CASE WHEN $2 THEN (o.created_at, o.id) > (r.created_at, r.id) ELSE (o.created_at, o.id) < (r.created_at, r.id) END)
				FROM comments r WHERE r.id = $1`, root, q.Newest, statuses).Scan(&pinned, &before)
			if err != nil {
				return ListPage{}, err
			}
			q.Cursor = nil
			switch {
			case pinned || before < limit:
			case before < policy.AroundMax:
				limit = before + 1
			default:
				var at time.Time
				if err := s.pool.QueryRow(ctx, `SELECT created_at FROM comments WHERE id = $1`, root).Scan(&at); err != nil {
					return ListPage{}, err
				}
				// Start the page at the target itself.
				if q.Newest {
					q.Cursor = &model.Cursor{At: at, ID: root + 1}
				} else {
					q.Cursor = &model.Cursor{At: at, ID: root - 1}
				}
			}
			if parent != nil {
				var rn int64
				if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM comments x JOIN comments t ON t.id = $1
					WHERE x.root_id = t.root_id AND x.status = ANY($2) AND (x.created_at, x.id) <= (t.created_at, t.id)`,
					q.Around, statuses).Scan(&rn); err != nil {
					return ListPage{}, err
				}
				repliesUpTo = rn
			}
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ListPage{}, err
		}
	}

	var page ListPage
	page.Partial = q.Cursor != nil && q.Around != 0
	var pinned []model.Comment
	if q.Cursor == nil {
		var err error
		pinned, err = collectComments(s.pool.Query(ctx, commentSelect+` WHERE c.thread_id = $1 AND c.root_id IS NULL
			AND c.pinned_at IS NOT NULL AND (c.status = ANY($2) OR `+hasVisibleReplies+`) ORDER BY c.pinned_at DESC, c.id`, q.ThreadID, statuses))
		if err != nil {
			return ListPage{}, err
		}
	}
	order := `ORDER BY c.created_at, c.id`
	cmp := `(c.created_at, c.id) > ($3, $4)`
	if q.Newest {
		order = `ORDER BY c.created_at DESC, c.id DESC`
		cmp = `(c.created_at, c.id) < ($3, $4)`
	}
	rest, err := collectComments(s.pool.Query(ctx, commentSelect+` WHERE c.thread_id = $1 AND c.root_id IS NULL AND c.pinned_at IS NULL
		AND (c.status = ANY($2) OR `+hasVisibleReplies+`)
		AND ($3::timestamptz IS NULL OR `+cmp+`) `+order+` LIMIT $5`,
		q.ThreadID, statuses, cursorAt(q.Cursor), cursorID(q.Cursor), limit+1))
	if err != nil {
		return ListPage{}, err
	}
	if len(rest) > limit {
		rest = rest[:limit]
		last := rest[len(rest)-1]
		page.Next = &model.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	page.Comments = append(pinned, rest...)
	if err := s.attachReplies(ctx, page.Comments, statuses, targetRoot, repliesUpTo); err != nil {
		return ListPage{}, err
	}
	return page, nil
}

// hasVisibleReplies keeps a top-level comment that is gone as a placeholder
// while replies under it are still shown; $2 holds the visible statuses.
const hasVisibleReplies = `EXISTS (SELECT 1 FROM comments r WHERE r.root_id = c.id AND r.status = ANY($2))`

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Store) attachReplies(ctx context.Context, top []model.Comment, statuses []string, targetRoot, upTo int64) error {
	if len(top) == 0 {
		return nil
	}
	ids := make([]int64, len(top))
	for i, c := range top {
		ids[i] = c.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT * FROM (SELECT `+commentCols+`,
		row_number() OVER (PARTITION BY c.root_id ORDER BY c.created_at, c.id) AS rn,
		count(*) OVER (PARTITION BY c.root_id) AS total`+commentFrom+`
		WHERE c.root_id = ANY($1) AND c.status = ANY($2)) x
		WHERE x.rn <= CASE WHEN x.root_id = $4::bigint THEN greatest($3::bigint, $5::bigint) ELSE $3::bigint END
		ORDER BY x.root_id, x.rn`, ids, statuses, policy.RepliesInline, targetRoot, min(upTo, policy.AroundMax))
	if err != nil {
		return err
	}
	byRoot := map[int64][]model.Comment{}
	totals := map[int64]int{}
	for rows.Next() {
		var rn, total int64
		c, err := scanComment(rows, &rn, &total)
		if err != nil {
			rows.Close()
			return err
		}
		byRoot[*c.RootID] = append(byRoot[*c.RootID], c)
		totals[*c.RootID] = int(total)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range top {
		top[i].Replies = byRoot[top[i].ID]
		top[i].ReplyCount = totals[top[i].ID]
	}
	return nil
}

// Replies returns a top-level comment's replies after the cursor, oldest
// first.
func (s *Store) Replies(ctx context.Context, rootID int64, statuses []string, cur *model.Cursor, limit int) ([]model.Comment, *model.Cursor, error) {
	if len(statuses) == 0 {
		statuses = []string{model.StatusPublished}
	}
	list, err := collectComments(s.pool.Query(ctx, commentSelect+` WHERE c.root_id = $1 AND c.status = ANY($2)
		AND ($3::timestamptz IS NULL OR (c.created_at, c.id) > ($3, $4)) ORDER BY c.created_at, c.id LIMIT $5`,
		rootID, statuses, cursorAt(cur), cursorID(cur), limit+1))
	if err != nil {
		return nil, nil, err
	}
	var next *model.Cursor
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		next = &model.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	return list, next, nil
}

// NewComment is a comment to store.
type NewComment struct {
	SiteID        int64
	ThreadKey     string
	ThreadURL     string
	ThreadTitle   string
	PagePublished *time.Time
	ParentID      *int64

	AuthorKind      string
	AuthorUserID    *int64
	AuthorName      string
	AuthorURL       string
	AuthorEmailEnc  []byte
	AuthorEmailHMAC []byte
	NotifyEmail     bool

	BodyMD        string
	BodyHTML      string
	RenderVersion int
	Status        string
	IPHMAC        []byte
	// Event, when set, is recorded as a system decision on the comment.
	Event *Event
}

// Event is a moderation_events row to record.
type Event struct {
	Action string
	Actor  *int64
	Detail map[string]any
}

// CreateComment stores a comment, creating its thread on the first
// comment, and runs what publishing triggers.
func (s *Store) CreateComment(ctx context.Context, n NewComment) (model.Comment, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var threadID int64
		var locked bool
		err := tx.QueryRow(ctx, `INSERT INTO threads (site_id, page_key, url, title, page_published_at)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (site_id, page_key) DO UPDATE SET page_key = EXCLUDED.page_key
			RETURNING id, locked_at IS NOT NULL`, n.SiteID, n.ThreadKey, n.ThreadURL, n.ThreadTitle, n.PagePublished).Scan(&threadID, &locked)
		if err != nil {
			return err
		}
		if locked {
			return ErrThreadLocked
		}
		var rootID *int64
		if n.ParentID != nil {
			var root int64
			err := tx.QueryRow(ctx, `SELECT coalesce(root_id, id) FROM comments WHERE id = $1 AND thread_id = $2 AND status = 'published'`,
				*n.ParentID, threadID).Scan(&root)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrBadParent
			}
			if err != nil {
				return err
			}
			rootID = &root
		}
		err = tx.QueryRow(ctx, `INSERT INTO comments (site_id, thread_id, root_id, parent_id, author_kind, author_user_id,
			author_name, author_email_enc, author_email_hmac, author_url, notify_email, body_md, body_html, render_version,
			status, ip_hmac, published_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			CASE WHEN $15 = 'published' THEN now() END) RETURNING id`,
			n.SiteID, threadID, rootID, n.ParentID, n.AuthorKind, n.AuthorUserID,
			nullString(n.AuthorName), n.AuthorEmailEnc, n.AuthorEmailHMAC, nullString(n.AuthorURL), n.NotifyEmail,
			n.BodyMD, n.BodyHTML, n.RenderVersion, n.Status, n.IPHMAC).Scan(&id)
		if err != nil {
			return err
		}
		if n.AuthorUserID != nil {
			if err := ensureSiteAuthor(ctx, tx, n.SiteID, *n.AuthorUserID); err != nil {
				return err
			}
		}
		if n.Status == model.StatusPublished {
			if err := recountThreads(ctx, tx, []int64{threadID}); err != nil {
				return err
			}
			if err := publishEffects(ctx, tx, id); err != nil {
				return err
			}
		}
		if n.Event != nil {
			return addEvent(ctx, tx, n.SiteID, &id, *n.Event)
		}
		return nil
	})
	if err != nil {
		return model.Comment{}, err
	}
	return s.Comment(ctx, id)
}

func addEvent(ctx context.Context, q querier, siteID int64, commentID *int64, e Event) error {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	var site *int64
	if siteID != 0 {
		site = &siteID
	}
	_, err = q.Exec(ctx, `INSERT INTO moderation_events (site_id, comment_id, actor_user_id, action, detail) VALUES ($1, $2, $3, $4, $5)`,
		site, commentID, e.Actor, e.Action, b)
	return err
}

// AddEvent records a moderation event outside a comment change.
func (s *Store) AddEvent(ctx context.Context, siteID int64, commentID *int64, e Event) error {
	return addEvent(ctx, s.pool, siteID, commentID, e)
}

// publishEffects runs once, when a comment is published for the first
// time, which the callers decide by published_at: the reply notification in the reader center and the reply mail to
// the parent's author (moderation.md section 6.1). Owner mail is gathered
// by the worker from site_members.last_notified_at.
func publishEffects(ctx context.Context, q querier, commentID int64) error {
	if _, err := q.Exec(ctx, `INSERT INTO notifications (user_id, kind, comment_id)
		SELECT p.author_user_id, 'reply', c.id FROM comments c
		JOIN comments p ON p.id = c.parent_id
		JOIN users pu ON pu.id = p.author_user_id
		WHERE c.id = $1 AND p.author_kind = 'user' AND p.author_user_id IS DISTINCT FROM c.author_user_id
		AND pu.suspended_at IS NULL AND pu.idp_suspended_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM thread_mutes tm WHERE tm.user_id = p.author_user_id AND tm.thread_id = c.thread_id)`,
		commentID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `INSERT INTO outbox (kind, to_enc, coalesce_key, locale, payload, next_attempt_at)
		SELECT 'reply', pu.email_enc, 'reply:u:' || pu.id, pu.locale,
			jsonb_build_object('comment_id', c.id, 'user_id', pu.id), `+coalescedAt(`'reply:u:' || pu.id`)+`
		FROM comments c
		JOIN comments p ON p.id = c.parent_id
		JOIN users pu ON pu.id = p.author_user_id
		WHERE c.id = $1 AND p.author_kind = 'user' AND p.author_user_id IS DISTINCT FROM c.author_user_id
		AND pu.suspended_at IS NULL AND pu.idp_suspended_at IS NULL AND pu.notify_replies AND pu.email_enc IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM thread_mutes tm WHERE tm.user_id = p.author_user_id AND tm.thread_id = c.thread_id)`,
		commentID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `INSERT INTO outbox (kind, to_enc, coalesce_key, locale, payload, next_attempt_at)
		SELECT 'guest_reply', g.email_enc, 'reply:g:' || encode(g.email_hmac, 'hex'), g.locale,
			jsonb_build_object('comment_id', c.id), `+coalescedAt(`'reply:g:' || encode(g.email_hmac, 'hex')`)+`
		FROM comments c
		JOIN comments p ON p.id = c.parent_id
		JOIN guest_emails g ON g.email_hmac = p.author_email_hmac
		WHERE c.id = $1 AND p.author_kind = 'guest' AND p.notify_email
		AND g.confirmed_at IS NOT NULL AND g.opted_out_at IS NULL
		AND c.author_email_hmac IS DISTINCT FROM p.author_email_hmac`, commentID)
	return err
}

// coalescedAt is when a new mail with this coalesce key goes out: with the
// key's queued mail if there is one, else at least the coalesce window
// after the key's last sent mail (moderation.md section 6.1).
func coalescedAt(key string) string {
	return `coalesce(
		(SELECT min(o.next_attempt_at) FROM outbox o WHERE o.status = 'queued' AND o.coalesce_key = ` + key + `),
		greatest(now(), (SELECT max(o.sent_at) FROM outbox o WHERE o.status = 'sent' AND o.coalesce_key = ` + key + `)
			+ interval '` + durationSQL(policy.MailCoalesceWindow) + `'))`
}

func durationSQL(d time.Duration) string { return time.Duration(d).String() }

// ModerateIn is a manager's action on one comment.
type ModerateIn struct {
	SiteID    int64
	CommentID int64
	Action    string // approve, hide, spam, restore, pin, unpin
	Actor     *int64
	// EventAction overrides the recorded action, for platform actions.
	EventAction string
	Detail      map[string]any
}

// Moderate applies a manager's action (moderation.md section 2).
func (s *Store) Moderate(ctx context.Context, in ModerateIn) (model.Comment, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error { return moderate(ctx, tx, in) })
	if err != nil {
		return model.Comment{}, err
	}
	return s.Comment(ctx, in.CommentID)
}

// ModerateMany applies one action to several comments, skipping those it
// does not apply to, and returns how many changed.
func (s *Store) ModerateMany(ctx context.Context, siteID int64, ids []int64, action string, actor *int64) (int, error) {
	n := 0
	for _, id := range ids {
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			return moderate(ctx, tx, ModerateIn{SiteID: siteID, CommentID: id, Action: action, Actor: actor})
		})
		switch {
		case err == nil:
			n++
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound):
		default:
			return n, err
		}
	}
	return n, nil
}

func moderate(ctx context.Context, tx pgx.Tx, in ModerateIn) error {
	var status string
	var root *int64
	var threadID int64
	var updated time.Time
	var pinned, neverPublished bool
	err := tx.QueryRow(ctx, `SELECT status, root_id, thread_id, updated_at, pinned_at IS NOT NULL, published_at IS NULL FROM comments
		WHERE id = $1 AND site_id = $2 FOR UPDATE`, in.CommentID, in.SiteID).Scan(&status, &root, &threadID, &updated, &pinned, &neverPublished)
	if err != nil {
		return notFound(err)
	}
	restorable := time.Since(updated) < policy.HiddenBodyKept
	next := ""
	switch in.Action {
	case "approve":
		if status == model.StatusPending || ((status == model.StatusSpam || status == model.StatusHidden) && restorable) {
			next = model.StatusPublished
		}
	case "restore":
		if (status == model.StatusSpam || status == model.StatusHidden) && restorable {
			next = model.StatusPublished
		}
	case "hide":
		if status == model.StatusPending || status == model.StatusPublished {
			next = model.StatusHidden
		}
	case "spam":
		if status == model.StatusPending || status == model.StatusPublished || status == model.StatusHidden {
			next = model.StatusSpam
		}
	case "pin", "unpin":
		if root != nil || status != model.StatusPublished || pinned == (in.Action == "pin") {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET pinned_at = CASE WHEN $2 THEN now() END, updated_at = now() WHERE id = $1`,
			in.CommentID, in.Action == "pin"); err != nil {
			return err
		}
		return addEvent(ctx, tx, in.SiteID, &in.CommentID, Event{Action: eventAction(in), Actor: in.Actor, Detail: in.Detail})
	}
	if next == "" {
		return ErrInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE comments SET status = $2, updated_at = now(),
		published_at = CASE WHEN $2 = 'published' THEN coalesce(published_at, now()) ELSE published_at END,
		pinned_at = CASE WHEN $2 = 'published' THEN pinned_at END WHERE id = $1`, in.CommentID, next); err != nil {
		return err
	}
	if err := recountThreads(ctx, tx, []int64{threadID}); err != nil {
		return err
	}
	if next == model.StatusPublished && neverPublished {
		if err := publishEffects(ctx, tx, in.CommentID); err != nil {
			return err
		}
	}
	return addEvent(ctx, tx, in.SiteID, &in.CommentID, Event{Action: eventAction(in), Actor: in.Actor, Detail: in.Detail})
}

func eventAction(in ModerateIn) string {
	if in.EventAction != "" {
		return in.EventAction
	}
	return in.Action
}

// EditIn is an author's edit.
type EditIn struct {
	CommentID     int64
	UserID        int64
	BodyMD        string
	BodyHTML      string
	RenderVersion int
	// Status is "" to keep the current status, or spam or pending when the
	// edit hits a rule (moderation.md section 2).
	Status string
	Rule   string
}

// EditComment saves the old body as a revision and the new one in place.
func (s *Store) EditComment(ctx context.Context, in EditIn) (model.Comment, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var old, status string
		var siteID, threadID int64
		err := tx.QueryRow(ctx, `SELECT body_md, status, site_id, thread_id FROM comments
			WHERE id = $1 AND author_user_id = $2 AND author_kind = 'user' FOR UPDATE`, in.CommentID, in.UserID).Scan(&old, &status, &siteID, &threadID)
		if err != nil {
			return notFound(err)
		}
		if status != model.StatusPublished && status != model.StatusPending {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO comment_revisions (comment_id, revised_at, body_md) VALUES ($1, clock_timestamp(), $2)`,
			in.CommentID, old); err != nil {
			return err
		}
		next := status
		if in.Status != "" {
			next = in.Status
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET body_md = $2, body_html = $3, render_version = $4, status = $5,
			pinned_at = CASE WHEN $5 = 'published' THEN pinned_at END,
			edited_at = now(), updated_at = now() WHERE id = $1`, in.CommentID, in.BodyMD, in.BodyHTML, in.RenderVersion, next); err != nil {
			return err
		}
		if next != status {
			if err := recountThreads(ctx, tx, []int64{threadID}); err != nil {
				return err
			}
		}
		detail := map[string]any{}
		if in.Rule != "" {
			detail["rule"] = in.Rule
			detail["status"] = next
		}
		return addEvent(ctx, tx, siteID, &in.CommentID, Event{Action: "author_edit", Actor: &in.UserID, Detail: detail})
	})
	if err != nil {
		return model.Comment{}, err
	}
	return s.Comment(ctx, in.CommentID)
}

// DeleteOwnComment is an author deleting their comment.
func (s *Store) DeleteOwnComment(ctx context.Context, commentID, userID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var siteID, threadID int64
		err := tx.QueryRow(ctx, `UPDATE comments SET status = 'deleted', pinned_at = NULL, updated_at = now()
			WHERE id = $1 AND author_user_id = $2 AND status <> 'deleted' RETURNING site_id, thread_id`, commentID, userID).Scan(&siteID, &threadID)
		if err != nil {
			return notFound(err)
		}
		if err := recountThreads(ctx, tx, []int64{threadID}); err != nil {
			return err
		}
		return addEvent(ctx, tx, siteID, &commentID, Event{Action: "author_delete", Actor: &userID})
	})
}

// LockThread locks or unlocks a thread.
func (s *Store) LockThread(ctx context.Context, siteID, threadID int64, locked bool, actor *int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE threads SET locked_at = CASE WHEN $3 THEN coalesce(locked_at, now()) END, updated_at = now()
			WHERE site_id = $1 AND id = $2`, siteID, threadID, locked)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		action := "unlock"
		if locked {
			action = "lock"
		}
		return addEvent(ctx, tx, siteID, nil, Event{Action: action, Actor: actor, Detail: map[string]any{"thread_id": threadID}})
	})
}

// RecentDuplicate reports whether the reader posted the same body within
// the duplicate window.
func (s *Store) RecentDuplicate(ctx context.Context, userID int64, body string) (bool, error) {
	var dup bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM comments WHERE md5(body_md) = md5($2) AND body_md = $2
		AND created_at > now() - $3 * interval '1 second' AND author_user_id = $1)`,
		userID, body, seconds(policy.DuplicateWindow)).Scan(&dup)
	return dup, err
}

// SitesWithBody counts the other sites where the same body appeared within
// the cross-site window.
func (s *Store) SitesWithBody(ctx context.Context, siteID int64, body string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT site_id) FROM comments WHERE md5(body_md) = md5($2) AND body_md = $2
		AND created_at > now() - $3 * interval '1 second' AND site_id <> $1`, siteID, body, seconds(policy.CrossSiteWindow)).Scan(&n)
	return n, err
}

// Banned reports whether any of the author's keys is banned on the site
// or across the platform.
func (s *Store) Banned(ctx context.Context, siteID int64, userID *int64, emailHMAC []byte, ipHMACs [][]byte) (bool, error) {
	var banned bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bans b WHERE (b.site_id = $1 OR b.site_id IS NULL)
		AND (b.expires_at IS NULL OR b.expires_at > now())
		AND (($2::bigint IS NOT NULL AND b.subject IS NOT NULL AND EXISTS (SELECT 1 FROM user_identities ui
				WHERE ui.user_id = $2 AND ui.issuer = b.issuer AND ui.subject = b.subject))
			OR ($3::bytea IS NOT NULL AND b.email_hmac = $3) OR b.ip_hmac = ANY($4)))`,
		siteID, userID, emailHMAC, ipHMACs).Scan(&banned)
	return banned, err
}

// Trusted reports whether an account meets the trusted account rule of
// moderation.md section 3.3.
func (s *Store) Trusted(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT `+trustedCond("$1::bigint", 2), userID,
		seconds(policy.TrustedAccountAge), policy.TrustedComments, policy.TrustedSites).Scan(&ok)
	return ok, err
}

// trustedCond is the trusted account rule as SQL for the account uid, with
// its three parameters at $base, $base+1 and $base+2: the account age in
// seconds, the comment count and the site count. Only comments on sites the
// account neither owns nor moderates count.
func trustedCond(uid string, base int) string {
	age, count, sites := fmt.Sprintf("$%d", base), fmt.Sprintf("$%d", base+1), fmt.Sprintf("$%d", base+2)
	theirs := `FROM comments tc WHERE tc.author_user_id = ` + uid + ` AND tc.status = 'published'
		AND NOT EXISTS (SELECT 1 FROM site_members tm WHERE tm.site_id = tc.site_id AND tm.user_id = ` + uid + `)`
	return `(EXISTS (SELECT 1 FROM users tu WHERE tu.id = ` + uid + ` AND tu.suspended_at IS NULL AND tu.idp_suspended_at IS NULL
			AND tu.created_at <= now() - ` + age + ` * interval '1 second')
		AND (SELECT count(*) ` + theirs + `) >= ` + count + `
		AND (SELECT count(DISTINCT tc.site_id) ` + theirs + `) >= ` + sites + `
		AND NOT EXISTS (SELECT 1 FROM moderation_events te JOIN comments tx ON tx.id = te.comment_id
			WHERE tx.author_user_id = ` + uid + ` AND te.action IN ('hide', 'spam', 'platform_hide'))
		AND NOT EXISTS (SELECT 1 FROM bans tb JOIN user_identities ti ON ti.issuer = tb.issuer AND ti.subject = tb.subject
			WHERE ti.user_id = ` + uid + `))`
}

// SpamDomains returns the platform's spam domain list.
func (s *Store) SpamDomains(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT domain FROM spam_domains ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AddSpamDomain lists a domain.
func (s *Store) AddSpamDomain(ctx context.Context, domain string, by int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO spam_domains (domain, created_by) VALUES ($1, $2) ON CONFLICT DO NOTHING`, domain, by)
	return err
}

// RemoveSpamDomain unlists a domain.
func (s *Store) RemoveSpamDomain(ctx context.Context, domain string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM spam_domains WHERE domain = $1`, domain)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
