package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ExportComment is a comment as it leaves in an export: every state, the
// structure, the source and, for guests, the encrypted address.
type ExportComment struct {
	ID          int64
	ThreadID    int64
	RootID      *int64
	ParentID    *int64
	AuthorKind  string
	AuthorID    string // site author ID of a reader
	AuthorName  string
	AuthorURL   string
	AuthorRef   string
	EmailEnc    []byte // guests only
	NotifyEmail bool
	BodyMD      string
	Status      string
	Pinned      bool
	Source      string
	SourceID    string
	CreatedAt   time.Time
	EditedAt    *time.Time
	PublishedAt *time.Time
}

// ExportThread is a thread as it leaves in an export.
type ExportThread struct {
	ID              int64
	Key             string
	URL             string
	Title           string
	PagePublishedAt *time.Time
	LockedAt        *time.Time
	CreatedAt       time.Time
}

// ExportSite reads a whole site for the blogger's export.
func (s *Store) ExportSite(ctx context.Context, siteID int64) ([]ExportThread, []ExportComment, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, page_key, url, title, page_published_at, locked_at, created_at
		FROM threads WHERE site_id = $1 ORDER BY id`, siteID)
	if err != nil {
		return nil, nil, err
	}
	threads, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExportThread, error) {
		var t ExportThread
		err := r.Scan(&t.ID, &t.Key, &t.URL, &t.Title, &t.PagePublishedAt, &t.LockedAt, &t.CreatedAt)
		return t, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.pool.Query(ctx, `SELECT c.id, c.thread_id, c.root_id, c.parent_id, c.author_kind, coalesce(sa.public_id::text, ''),
		CASE WHEN c.author_kind = 'user' THEN u.display_name ELSE coalesce(c.author_name, '') END,
		CASE WHEN c.author_kind = 'user' THEN coalesce(u.homepage_url, '') ELSE coalesce(c.author_url, '') END,
		coalesce(c.author_ref, ''), CASE WHEN c.author_kind = 'guest' THEN c.author_email_enc END, c.notify_email,
		CASE WHEN c.status = 'deleted' THEN '' ELSE c.body_md END,
		c.status, c.pinned_at IS NOT NULL, c.source, coalesce(c.source_id, ''), c.created_at, c.edited_at, c.published_at
		FROM comments c LEFT JOIN users u ON u.id = c.author_user_id
		LEFT JOIN site_authors sa ON sa.site_id = c.site_id AND sa.user_id = c.author_user_id
		WHERE c.site_id = $1 ORDER BY c.id`, siteID)
	if err != nil {
		return nil, nil, err
	}
	comments, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExportComment, error) {
		var c ExportComment
		err := r.Scan(&c.ID, &c.ThreadID, &c.RootID, &c.ParentID, &c.AuthorKind, &c.AuthorID, &c.AuthorName, &c.AuthorURL,
			&c.AuthorRef, &c.EmailEnc, &c.NotifyEmail, &c.BodyMD, &c.Status, &c.Pinned, &c.Source, &c.SourceID,
			&c.CreatedAt, &c.EditedAt, &c.PublishedAt)
		return c, err
	})
	return threads, comments, err
}
