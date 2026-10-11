package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Import is an imports row without its file.
type Import struct {
	ID         int64
	SiteID     int64
	Format     string
	Status     string
	Mapping    json.RawMessage
	Stats      json.RawMessage
	Error      string
	CreatedAt  time.Time
	FinishedAt *time.Time
	HasFile    bool
}

// CreateImport stores an uploaded file.
func (s *Store) CreateImport(ctx context.Context, siteID int64, format string, file []byte, by int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO imports (site_id, format, file, created_by) VALUES ($1, $2, $3, $4) RETURNING id`,
		siteID, format, file, by).Scan(&id)
	return id, err
}

const importColumns = `id, site_id, format, status, mapping, stats, coalesce(error, ''), created_at, finished_at, file IS NOT NULL`

func scanImport(row pgx.Row) (Import, error) {
	var im Import
	err := row.Scan(&im.ID, &im.SiteID, &im.Format, &im.Status, &im.Mapping, &im.Stats, &im.Error, &im.CreatedAt, &im.FinishedAt, &im.HasFile)
	return im, notFound(err)
}

// Import returns an import of a site.
func (s *Store) Import(ctx context.Context, siteID, id int64) (Import, error) {
	return scanImport(s.pool.QueryRow(ctx, `SELECT `+importColumns+` FROM imports WHERE site_id = $1 AND id = $2`, siteID, id))
}

// ImportFile returns an import's uploaded file.
func (s *Store) ImportFile(ctx context.Context, id int64) ([]byte, error) {
	var b []byte
	err := s.pool.QueryRow(ctx, `SELECT file FROM imports WHERE id = $1 AND file IS NOT NULL`, id).Scan(&b)
	return b, notFound(err)
}

// SetImportPreview records a preview, and the mapping it was made with.
func (s *Store) SetImportPreview(ctx context.Context, id int64, mapping, stats []byte, failed string) error {
	status := "previewed"
	if failed != "" {
		status = "failed"
	}
	_, err := s.pool.Exec(ctx, `UPDATE imports SET status = $2, mapping = $3, stats = $4, error = nullif($5, ''),
		finished_at = CASE WHEN $2 = 'failed' THEN now() END WHERE id = $1 AND status IN ('uploaded', 'previewed')`,
		id, status, mapping, stats, failed)
	return err
}

// StartImport queues a previewed import for the worker.
func (s *Store) StartImport(ctx context.Context, siteID, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE imports SET status = 'running' WHERE site_id = $1 AND id = $2 AND status = 'previewed'`, siteID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrInvalid
	}
	return err
}

// NextImport returns a running import for the worker, or ErrNotFound.
func (s *Store) NextImport(ctx context.Context) (Import, error) {
	return scanImport(s.pool.QueryRow(ctx, `SELECT `+importColumns+` FROM imports WHERE status = 'running' ORDER BY id LIMIT 1`))
}

// FinishImport records an import's outcome.
func (s *Store) FinishImport(ctx context.Context, id int64, stats []byte, failed string) error {
	status := "done"
	if failed != "" {
		status = "failed"
	}
	if len([]rune(failed)) > 2000 {
		failed = string([]rune(failed)[:2000])
	}
	_, err := s.pool.Exec(ctx, `UPDATE imports SET status = $2, stats = $3, error = nullif($4, ''), finished_at = now() WHERE id = $1`,
		id, status, stats, failed)
	return err
}

// ImportThread is a thread an import writes to.
type ImportThread struct {
	Key         string
	URL         string
	Title       string
	PublishedAt *time.Time
	Locked      bool
}

// ImportComment is a comment an import writes, already rendered.
type ImportComment struct {
	SourceID       string
	ParentSourceID string
	RootSourceID   string
	ThreadKey      string
	AuthorKind     string // guest or external
	AuthorName     string
	AuthorURL      string
	AuthorRef      string
	EmailEnc       []byte
	EmailHMAC      []byte
	BodyMD         string
	BodyHTML       string
	RenderVersion  int
	Status         string
	Pinned         bool
	CreatedAt      time.Time
	EditedAt       *time.Time
}

// ImportStats counts what an import did.
type ImportStats struct {
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// ImportComments writes threads and comments in one transaction. Comments
// already imported from the same source are skipped, so running an import
// twice adds nothing. Parents must come before their replies.
func (s *Store) ImportComments(ctx context.Context, siteID int64, source string, threads []ImportThread, comments []ImportComment) (ImportStats, error) {
	var st ImportStats
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		threadIDs := map[string]int64{}
		for _, t := range threads {
			var id int64
			err := tx.QueryRow(ctx, `INSERT INTO threads (site_id, page_key, url, title, page_published_at, locked_at)
				VALUES ($1, $2, $3, $4, $5, CASE WHEN $6 THEN now() END)
				ON CONFLICT (site_id, page_key) DO UPDATE SET page_key = EXCLUDED.page_key RETURNING id`,
				siteID, t.Key, t.URL, t.Title, t.PublishedAt, t.Locked).Scan(&id)
			if err != nil {
				return err
			}
			threadIDs[t.Key] = id
		}
		ids := map[string]int64{}
		touched := map[int64]bool{}
		for _, c := range comments {
			threadID, ok := threadIDs[c.ThreadKey]
			if !ok {
				st.Failed++
				continue
			}
			var existing int64
			err := tx.QueryRow(ctx, `SELECT id FROM comments WHERE site_id = $1 AND source = $2 AND source_id = $3`, siteID, source, c.SourceID).Scan(&existing)
			if err == nil {
				ids[c.SourceID] = existing
				st.Skipped++
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			var root, parent *int64
			if c.ParentSourceID != "" {
				p, okP := ids[c.ParentSourceID]
				r, okR := ids[c.RootSourceID]
				if okP && okR {
					parent, root = &p, &r
				}
			}
			var pinned *time.Time
			if c.Pinned && root == nil && c.Status == "published" {
				pinned = &c.CreatedAt
			}
			var published *time.Time
			if c.Status == "published" {
				published = &c.CreatedAt
			}
			var id int64
			if err := tx.QueryRow(ctx, `INSERT INTO comments (site_id, thread_id, root_id, parent_id, author_kind, author_name,
				author_email_enc, author_email_hmac, author_url, author_ref, body_md, body_html, render_version, status, pinned_at,
				source, source_id, created_at, updated_at, edited_at, published_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $18, $19, $20) RETURNING id`,
				siteID, threadID, root, parent, c.AuthorKind, c.AuthorName, c.EmailEnc, c.EmailHMAC, nullString(c.AuthorURL),
				nullString(c.AuthorRef), c.BodyMD, c.BodyHTML, c.RenderVersion, c.Status, pinned, source, c.SourceID,
				c.CreatedAt, c.EditedAt, published).Scan(&id); err != nil {
				return err
			}
			ids[c.SourceID] = id
			touched[threadID] = true
			st.Inserted++
		}
		var list []int64
		for id := range touched {
			list = append(list, id)
		}
		return recountThreads(ctx, tx, list)
	})
	return st, err
}
