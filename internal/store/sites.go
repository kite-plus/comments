package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

const siteColumns = `s.id, s.public_id::text, s.name, s.status, coalesce(s.status_note, ''), s.guest_policy, s.member_policy,
	s.sort_order, s.close_after_days, s.query_keys, s.allow_localhost, s.owner_notify, s.blocked_words, s.created_by,
	s.verified_at, s.approved_at, s.deleted_at, s.created_at, s.updated_at`

func scanSite(row pgx.Row) (model.Site, error) {
	var x model.Site
	err := row.Scan(&x.ID, &x.PublicID, &x.Name, &x.Status, &x.StatusNote, &x.GuestPolicy, &x.MemberPolicy,
		&x.SortOrder, &x.CloseAfterDays, &x.QueryKeys, &x.AllowLocalhost, &x.OwnerNotify, &x.BlockedWords, &x.CreatedBy,
		&x.VerifiedAt, &x.ApprovedAt, &x.DeletedAt, &x.CreatedAt, &x.UpdatedAt)
	if x.QueryKeys == nil {
		x.QueryKeys = []string{}
	}
	if x.BlockedWords == nil {
		x.BlockedWords = []string{}
	}
	return x, notFound(err)
}

func (s *Store) withOrigins(ctx context.Context, q querier, x model.Site) (model.Site, error) {
	rows, err := q.Query(ctx, `SELECT origin, is_primary, verified_at, last_checked_at, failing_since, created_at
		FROM site_origins WHERE site_id = $1 ORDER BY is_primary DESC, created_at, origin`, x.ID)
	if err != nil {
		return x, err
	}
	x.Origins, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Origin, error) {
		var o model.Origin
		err := r.Scan(&o.Origin, &o.Primary, &o.VerifiedAt, &o.LastCheckedAt, &o.FailingSince, &o.CreatedAt)
		return o, err
	})
	return x, err
}

// NewSite is a site to register.
type NewSite struct {
	Name      string
	Origins   []string // normalized; the first is primary
	CreatedBy *int64
	// Verified marks every origin verified, for the development seed.
	Verified bool
	// PublicID fixes the site ID, for the development seed.
	PublicID string
}

// CreateSite registers a site with its owner. An origin that already
// belongs to a site gives ErrConflict.
func (s *Store) CreateSite(ctx context.Context, n NewSite) (model.Site, error) {
	var x model.Site
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		status := model.SiteUnverified
		var verified *time.Time
		if n.Verified {
			now := time.Now()
			verified = &now
			status = model.SiteActive
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO sites (public_id, name, status, created_by, verified_at, approved_at)
			VALUES (coalesce($1::uuid, gen_random_uuid()), $2, $3, $4, $5, $5) RETURNING id`,
			nullString(n.PublicID), n.Name, status, n.CreatedBy, verified).Scan(&id)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return err
		}
		for i, o := range n.Origins {
			if _, err := tx.Exec(ctx, `INSERT INTO site_origins (origin, site_id, is_primary, verified_at, last_checked_at)
				VALUES ($1, $2, $3, $4, $4)`, o, id, i == 0, verified); err != nil {
				if isUniqueViolation(err) {
					return ErrConflict
				}
				return err
			}
		}
		if n.CreatedBy != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO site_members (site_id, user_id, role) VALUES ($1, $2, 'owner')`, id, *n.CreatedBy); err != nil {
				return err
			}
		}
		x, err = scanSite(tx.QueryRow(ctx, `SELECT `+siteColumns+` FROM sites s WHERE s.id = $1`, id))
		if err != nil {
			return err
		}
		x, err = s.withOrigins(ctx, tx, x)
		return err
	})
	return x, err
}

// Site returns a site by public ID, with its origins.
func (s *Store) Site(ctx context.Context, publicID string) (model.Site, error) {
	x, err := scanSite(s.pool.QueryRow(ctx, `SELECT `+siteColumns+` FROM sites s WHERE s.public_id::text = $1`, publicID))
	if err != nil {
		return x, err
	}
	return s.withOrigins(ctx, s.pool, x)
}

// SiteByID returns a site by internal ID, with its origins.
func (s *Store) SiteByID(ctx context.Context, id int64) (model.Site, error) {
	x, err := scanSite(s.pool.QueryRow(ctx, `SELECT `+siteColumns+` FROM sites s WHERE s.id = $1`, id))
	if err != nil {
		return x, err
	}
	return s.withOrigins(ctx, s.pool, x)
}

// MemberSite is a site with the reader's role in it.
type MemberSite struct {
	model.Site
	Role          string
	PendingCount  int
	ReportedCount int
}

// SitesOf lists the sites a reader owns or moderates.
func (s *Store) SitesOf(ctx context.Context, userID int64) ([]MemberSite, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+siteColumns+`, m.role,
		(SELECT count(*) FROM comments c WHERE c.site_id = s.id AND c.status = 'pending'),
		(SELECT count(DISTINCT r.comment_id) FROM reports r WHERE r.site_id = s.id AND r.status = 'open')
		FROM site_members m JOIN sites s ON s.id = m.site_id WHERE m.user_id = $1 ORDER BY s.created_at`, userID)
	if err != nil {
		return nil, err
	}
	var out []MemberSite
	for rows.Next() {
		var x model.Site
		var m MemberSite
		if err := rows.Scan(&x.ID, &x.PublicID, &x.Name, &x.Status, &x.StatusNote, &x.GuestPolicy, &x.MemberPolicy,
			&x.SortOrder, &x.CloseAfterDays, &x.QueryKeys, &x.AllowLocalhost, &x.OwnerNotify, &x.BlockedWords, &x.CreatedBy,
			&x.VerifiedAt, &x.ApprovedAt, &x.DeletedAt, &x.CreatedAt, &x.UpdatedAt, &m.Role, &m.PendingCount, &m.ReportedCount); err != nil {
			rows.Close()
			return nil, err
		}
		m.Site = x
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Site, err = s.withOrigins(ctx, s.pool, out[i].Site); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Role returns the reader's role in a site, or "".
func (s *Store) Role(ctx context.Context, siteID, userID int64) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM site_members WHERE site_id = $1 AND user_id = $2`, siteID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// AddMember makes a reader an owner or moderator of a site.
func (s *Store) AddMember(ctx context.Context, siteID, userID int64, role string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO site_members (site_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (site_id, user_id) DO UPDATE SET role = $3`, siteID, userID, role)
	return err
}

// SiteSettings changes the fields that are set.
type SiteSettings struct {
	Name           *string
	GuestPolicy    *string
	MemberPolicy   *string
	SortOrder      *string
	CloseAfterDays **int
	QueryKeys      *[]string
	AllowLocalhost *bool
	OwnerNotify    *string
	BlockedWords   *[]string
}

// UpdateSite changes a site's settings.
func (s *Store) UpdateSite(ctx context.Context, siteID int64, p SiteSettings) (model.Site, error) {
	setClose := p.CloseAfterDays != nil
	var closeDays *int
	if setClose {
		closeDays = *p.CloseAfterDays
	}
	_, err := s.pool.Exec(ctx, `UPDATE sites SET
		name = coalesce($2, name),
		guest_policy = coalesce($3, guest_policy),
		member_policy = coalesce($4, member_policy),
		sort_order = coalesce($5, sort_order),
		close_after_days = CASE WHEN $6 THEN $7 ELSE close_after_days END,
		query_keys = coalesce($8, query_keys),
		allow_localhost = coalesce($9, allow_localhost),
		owner_notify = coalesce($10, owner_notify),
		blocked_words = coalesce($11, blocked_words),
		updated_at = now()
		WHERE id = $1`, siteID, p.Name, p.GuestPolicy, p.MemberPolicy, p.SortOrder, setClose, closeDays,
		p.QueryKeys, p.AllowLocalhost, p.OwnerNotify, p.BlockedWords)
	if err != nil {
		return model.Site{}, err
	}
	return s.SiteByID(ctx, siteID)
}

// AddOrigin adds an unverified alias origin. An origin that belongs to a
// site already gives ErrConflict.
func (s *Store) AddOrigin(ctx context.Context, siteID int64, origin string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO site_origins (origin, site_id) VALUES ($1, $2)`, origin, siteID)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// OriginOwner returns the site an origin belongs to.
func (s *Store) OriginOwner(ctx context.Context, origin string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT site_id FROM site_origins WHERE origin = $1`, origin).Scan(&id)
	return id, notFound(err)
}

// RemoveOrigin removes an alias; the primary origin stays.
func (s *Store) RemoveOrigin(ctx context.Context, siteID int64, origin string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM site_origins WHERE site_id = $1 AND origin = $2 AND NOT is_primary`, siteID, origin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return refreshSiteStatus(ctx, tx, siteID)
	})
}

// CreateVerification stores a new verification token's hash.
func (s *Store) CreateVerification(ctx context.Context, siteID, userID int64, tokenHash []byte, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO site_verifications (site_id, token_hash, created_by, expires_at) VALUES ($1, $2, $3, $4)`,
		siteID, tokenHash, userID, expires)
	return err
}

// VerificationHashes returns the hashes of a site's live tokens.
func (s *Store) VerificationHashes(ctx context.Context, siteID int64) ([][]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT token_hash FROM site_verifications WHERE site_id = $1 AND expires_at > now()`, siteID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[[]byte])
}

// RecordVerificationError notes the last failure on a site's tokens.
func (s *Store) RecordVerificationError(ctx context.Context, siteID int64, msg string) error {
	if len([]rune(msg)) > 500 {
		msg = string([]rune(msg)[:500])
	}
	_, err := s.pool.Exec(ctx, `UPDATE site_verifications SET last_error = $2 WHERE site_id = $1 AND expires_at > now()`, siteID, msg)
	return err
}

// MarkOriginVerified records a successful check of one origin, keeping the
// hash of the token that proved it for later rechecks, and moves the site
// forward: an unverified site goes to review, or to active when
// review is off or it was approved before.
func (s *Store) MarkOriginVerified(ctx context.Context, siteID int64, origin string, proofHash []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE site_origins SET verified_at = coalesce(verified_at, now()), last_checked_at = now(),
			failing_since = NULL, proof_hash = coalesce($3, proof_hash) WHERE site_id = $1 AND origin = $2`, siteID, origin, proofHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE sites SET verified_at = coalesce(verified_at, now()), updated_at = now() WHERE id = $1`, siteID); err != nil {
			return err
		}
		return refreshSiteStatus(ctx, tx, siteID)
	})
}

// refreshSiteStatus moves a site between unverified, pending_review and
// active after its origins changed. Suspended and deleted sites stay.
func refreshSiteStatus(ctx context.Context, q querier, siteID int64) error {
	_, err := q.Exec(ctx, `UPDATE sites s SET status = CASE
			WHEN NOT EXISTS (SELECT 1 FROM site_origins o WHERE o.site_id = s.id AND o.verified_at IS NOT NULL) THEN 'unverified'
			WHEN s.approved_at IS NOT NULL OR NOT $2 THEN 'active'
			ELSE 'pending_review' END,
		approved_at = CASE WHEN NOT $2 THEN coalesce(s.approved_at, now()) ELSE s.approved_at END,
		updated_at = now()
		WHERE s.id = $1 AND s.status IN ('unverified', 'pending_review', 'active')`, siteID, policy.SiteApprovalRequired)
	return err
}

// ApproveSite is the platform's approval of a site in review.
func (s *Store) ApproveSite(ctx context.Context, siteID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sites SET approved_at = coalesce(approved_at, now()), updated_at = now()
			WHERE id = $1 AND status IN ('unverified', 'pending_review', 'active')`, siteID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInvalid
		}
		return refreshSiteStatus(ctx, tx, siteID)
	})
}

// SuspendSite stops a site's widget from taking comments.
func (s *Store) SuspendSite(ctx context.Context, siteID int64, note string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sites SET status = 'suspended', status_note = $2, updated_at = now()
		WHERE id = $1 AND status <> 'deleted'`, siteID, nullString(note))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrInvalid
	}
	return err
}

// UnsuspendSite lifts a suspension.
func (s *Store) UnsuspendSite(ctx context.Context, siteID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sites SET status = 'unverified', status_note = NULL, updated_at = now()
			WHERE id = $1 AND status = 'suspended'`, siteID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInvalid
		}
		return refreshSiteStatus(ctx, tx, siteID)
	})
}

// DeleteSite starts the recovery window; the worker purges it afterwards.
func (s *Store) DeleteSite(ctx context.Context, siteID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sites SET status = 'deleted', deleted_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'deleted'`, siteID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrInvalid
	}
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM site_sessions WHERE site_id = $1`, siteID)
	}
	return err
}

// RestoreSite brings a deleted site back within the recovery window.
func (s *Store) RestoreSite(ctx context.Context, siteID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE sites SET status = 'unverified', deleted_at = NULL, status_note = NULL, updated_at = now()
			WHERE id = $1 AND status = 'deleted' AND deleted_at > now() - $2 * interval '1 second'`, siteID, seconds(policy.DeletedSitesKept))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrInvalid
		}
		return refreshSiteStatus(ctx, tx, siteID)
	})
}

// AdminSites lists sites for the platform, optionally by status.
func (s *Store) AdminSites(ctx context.Context, status string, cur *model.Cursor, limit int) ([]model.Site, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+siteColumns+` FROM sites s
		WHERE ($1 = '' OR s.status = $1) AND ($2::timestamptz IS NULL OR (s.created_at, s.id) < ($2, $3))
		ORDER BY s.created_at DESC, s.id DESC LIMIT $4`, status, cursorAt(cur), cursorID(cur), limit)
	if err != nil {
		return nil, err
	}
	sites, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Site, error) { return scanSite(r) })
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if sites[i], err = s.withOrigins(ctx, s.pool, sites[i]); err != nil {
			return nil, err
		}
	}
	return sites, nil
}

func cursorAt(c *model.Cursor) *time.Time {
	if c == nil {
		return nil
	}
	return &c.At
}

func cursorID(c *model.Cursor) int64 {
	if c == nil {
		return 0
	}
	return c.ID
}

// DueOrigin is a verified origin due for its periodic recheck.
type DueOrigin struct {
	SiteID       int64
	Origin       string
	ProofHash    []byte
	FailingSince *time.Time
}

// OriginsDue lists verified origins last checked before the recheck
// period (sites.md section 2).
func (s *Store) OriginsDue(ctx context.Context, limit int) ([]DueOrigin, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.site_id, o.origin, o.proof_hash, o.failing_since FROM site_origins o
		JOIN sites st ON st.id = o.site_id
		WHERE o.verified_at IS NOT NULL AND st.status IN ('pending_review', 'active', 'suspended')
		AND coalesce(o.last_checked_at, o.verified_at) < now() - $1 * interval '1 second'
		ORDER BY coalesce(o.last_checked_at, o.verified_at) LIMIT $2`, seconds(policy.ReverifyEvery), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DueOrigin, error) {
		var d DueOrigin
		err := r.Scan(&d.SiteID, &d.Origin, &d.ProofHash, &d.FailingSince)
		return d, err
	})
}

// RecordRecheck stores a recheck's outcome. A failure that has lasted the
// grace period takes the origin off the verified list; it reports whether
// this was the origin's first failure, so the owner can be told.
func (s *Store) RecordRecheck(ctx context.Context, siteID int64, origin string, ok bool) (firstFailure, dropped bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if ok {
			_, err := tx.Exec(ctx, `UPDATE site_origins SET last_checked_at = now(), failing_since = NULL WHERE site_id = $1 AND origin = $2`, siteID, origin)
			return err
		}
		var failing *time.Time
		if err := tx.QueryRow(ctx, `UPDATE site_origins SET last_checked_at = now(), failing_since = coalesce(failing_since, now())
			WHERE site_id = $1 AND origin = $2 RETURNING failing_since`, siteID, origin).Scan(&failing); err != nil {
			return notFound(err)
		}
		firstFailure = failing != nil && time.Since(*failing) < time.Minute
		if failing != nil && time.Since(*failing) >= policy.ReverifyGrace {
			if _, err := tx.Exec(ctx, `UPDATE site_origins SET verified_at = NULL WHERE site_id = $1 AND origin = $2`, siteID, origin); err != nil {
				return err
			}
			dropped = true
			return refreshSiteStatus(ctx, tx, siteID)
		}
		return nil
	})
	return firstFailure, dropped, err
}
