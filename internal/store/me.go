package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
)

// withPlace wraps a comment query to add the site and thread, for lists
// that span sites.
func withPlace(inner string) string {
	return `SELECT x.*, st.name, st.public_id::text, t.url, t.title FROM (` + inner + `) x
		JOIN sites st ON st.id = x.site_id JOIN threads t ON t.id = x.thread_id`
}

func collectPlaced(rows pgx.Rows, err error, extra func() []any) ([]model.Comment, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Comment
	for rows.Next() {
		var e []any
		if extra != nil {
			e = extra()
		}
		var site, sitePublic, url, title string
		c, err := scanComment(rows, append([]any{&site, &sitePublic, &url, &title}, e...)...)
		if err != nil {
			return nil, err
		}
		c.SiteName, c.SitePublic, c.ThreadURL, c.ThreadTitle = site, sitePublic, url, title
		out = append(out, c)
	}
	return out, rows.Err()
}

// MyComments lists a reader's comments on every site, newest first, in
// every state but deleted; sitePublicID narrows it to one site.
func (s *Store) MyComments(ctx context.Context, userID int64, sitePublicID string, cur *model.Cursor, limit int) ([]model.Comment, *model.Cursor, error) {
	rows, err := s.pool.Query(ctx, withPlace(commentSelect+` WHERE c.author_user_id = $1 AND c.status <> 'deleted'
		AND ($2 = '' OR c.site_id = (SELECT id FROM sites WHERE public_id::text = $2))
		AND ($3::timestamptz IS NULL OR (c.created_at, c.id) < ($3, $4))
		ORDER BY c.created_at DESC, c.id DESC LIMIT $5`)+` ORDER BY x.created_at DESC, x.id DESC`,
		userID, sitePublicID, cursorAt(cur), cursorID(cur), limit+1)
	list, err := collectPlaced(rows, err, nil)
	if err != nil {
		return nil, nil, err
	}
	var next *model.Cursor
	if limit > 0 && len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		next = &model.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	return list, next, nil
}

// Notification is a reply in the reader center.
type Notification struct {
	ID        int64
	Kind      string
	CreatedAt time.Time
	ReadAt    *time.Time
	Comment   model.Comment
}

// Notifications lists a reader's notifications, newest first, leaving out
// replies that are no longer published.
func (s *Store) Notifications(ctx context.Context, userID int64, cur *model.Cursor, limit int) ([]Notification, *model.Cursor, int, error) {
	nrows, err := s.pool.Query(ctx, `SELECT n.id, n.kind, n.created_at, n.read_at, n.comment_id FROM notifications n
		JOIN comments c ON c.id = n.comment_id AND c.status = 'published'
		WHERE n.user_id = $1 AND ($2::timestamptz IS NULL OR (n.created_at, n.id) < ($2, $3))
		ORDER BY n.created_at DESC, n.id DESC LIMIT $4`, userID, cursorAt(cur), cursorID(cur), limit+1)
	if err != nil {
		return nil, nil, 0, err
	}
	var out []Notification
	var ids []int64
	for nrows.Next() {
		var n Notification
		if err := nrows.Scan(&n.ID, &n.Kind, &n.CreatedAt, &n.ReadAt, &n.Comment.ID); err != nil {
			nrows.Close()
			return nil, nil, 0, err
		}
		out = append(out, n)
		ids = append(ids, n.Comment.ID)
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return nil, nil, 0, err
	}
	var next *model.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = &model.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	rows, err := s.pool.Query(ctx, withPlace(commentSelect+` WHERE c.id = ANY($1)`), ids)
	list, err := collectPlaced(rows, err, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	byID := map[int64]model.Comment{}
	for _, c := range list {
		byID[c.ID] = c
	}
	for i := range out {
		out[i].Comment = byID[out[i].Comment.ID]
	}
	var unread int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications n JOIN comments c ON c.id = n.comment_id AND c.status = 'published'
		WHERE n.user_id = $1 AND n.read_at IS NULL`, userID).Scan(&unread)
	return out, next, unread, err
}

// MarkRead marks notifications read; ids nil marks all.
func (s *Store) MarkRead(ctx context.Context, userID int64, ids []int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL
		AND ($2::bigint[] IS NULL OR id = ANY($2))`, userID, ids)
	return err
}

// Mute stops reply notifications from a thread.
func (s *Store) Mute(ctx context.Context, userID, threadID int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO thread_mutes (user_id, thread_id) SELECT $1, id FROM threads WHERE id = $2
		ON CONFLICT DO NOTHING`, userID, threadID)
	return err
}

// Unmute restores reply notifications from a thread.
func (s *Store) Unmute(ctx context.Context, userID, threadID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM thread_mutes WHERE user_id = $1 AND thread_id = $2`, userID, threadID)
	return err
}

// Muted lists the threads a reader muted.
func (s *Store) Muted(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT thread_id FROM thread_mutes WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// ThreadExists reports whether a thread exists, for the mute endpoints.
func (s *Store) ThreadExists(ctx context.Context, threadID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM threads WHERE id = $1)`, threadID).Scan(&ok)
	return ok, err
}
