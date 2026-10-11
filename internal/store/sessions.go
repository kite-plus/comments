package store

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

// Login transaction modes (data-model.md section 3.2).
const (
	ModePopup    = "popup"
	ModeRedirect = "redirect"
	ModeConsole  = "console"
)

// LoginTx is a row of login_transactions.
type LoginTx struct {
	ID           int64
	Mode         string
	SiteID       *int64
	Challenge    []byte
	ReturnTo     string
	StateHash    []byte
	BinderHash   []byte
	Nonce        string
	PKCEVerifier string
	UserID       *int64
	IDPSID       string
	Status       string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// CreateLoginTx records a new login transaction. A challenge seen before
// gives ErrConflict.
func (s *Store) CreateLoginTx(ctx context.Context, t LoginTx) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO login_transactions
		(mode, site_id, challenge, return_to, state_hash, binder_hash, oidc_nonce, pkce_verifier, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		t.Mode, t.SiteID, t.Challenge, nullString(t.ReturnTo), t.StateHash, t.BinderHash, t.Nonce, t.PKCEVerifier, t.ExpiresAt).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrConflict
	}
	return id, err
}

const loginTxColumns = `id, mode, site_id, challenge, coalesce(return_to, ''), state_hash, binder_hash, oidc_nonce,
	pkce_verifier, user_id, coalesce(idp_sid, ''), status, created_at, expires_at`

func scanLoginTx(row pgx.Row) (LoginTx, error) {
	var t LoginTx
	err := row.Scan(&t.ID, &t.Mode, &t.SiteID, &t.Challenge, &t.ReturnTo, &t.StateHash, &t.BinderHash, &t.Nonce,
		&t.PKCEVerifier, &t.UserID, &t.IDPSID, &t.Status, &t.CreatedAt, &t.ExpiresAt)
	return t, notFound(err)
}

// LoginTxByState finds a transaction by the hash of its OIDC state.
func (s *Store) LoginTxByState(ctx context.Context, stateHash []byte) (LoginTx, error) {
	return scanLoginTx(s.pool.QueryRow(ctx, `SELECT `+loginTxColumns+` FROM login_transactions WHERE state_hash = $1`, stateHash))
}

// LoginTx finds a transaction by ID.
func (s *Store) LoginTx(ctx context.Context, id int64) (LoginTx, error) {
	return scanLoginTx(s.pool.QueryRow(ctx, `SELECT `+loginTxColumns+` FROM login_transactions WHERE id = $1`, id))
}

// AuthenticateLoginTx moves a pending, unexpired transaction to
// authenticated, recording who signed in and the identity session.
func (s *Store) AuthenticateLoginTx(ctx context.Context, id, userID int64, sid string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE login_transactions SET status = 'authenticated', user_id = $2, idp_sid = $3,
		authenticated_at = now() WHERE id = $1 AND status = 'pending' AND expires_at > now()`, id, userID, sid)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrLoginGone
	}
	return err
}

// ConfirmLoginTx moves an authenticated redirect transaction bound to
// binderHash to confirmed and returns it.
func (s *Store) ConfirmLoginTx(ctx context.Context, id int64, binderHash []byte) (LoginTx, error) {
	var t LoginTx
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		t, err = scanLoginTx(tx.QueryRow(ctx, `SELECT `+loginTxColumns+` FROM login_transactions WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if !bytes.Equal(t.BinderHash, binderHash) || t.Mode != ModeRedirect || t.Status != "authenticated" || !t.ExpiresAt.After(time.Now()) {
			return ErrInvalid
		}
		_, err = tx.Exec(ctx, `UPDATE login_transactions SET status = 'confirmed', confirmed_at = now() WHERE id = $1`, id)
		return err
	})
	return t, err
}

// SiteSessionIn is a new site session's token hash and description.
type SiteSessionIn struct {
	TokenHash   []byte
	ClientLabel string
	ExpiresAt   time.Time
}

// RedeemWidgetLogin exchanges a finished widget transaction for a site
// session (login.md section 4.2 step 7). It returns ErrLoginPending when
// the challenge is unknown or the login not finished, and ErrLoginGone when
// it expired or was redeemed.
func (s *Store) RedeemWidgetLogin(ctx context.Context, siteID int64, challenge []byte, in SiteSessionIn) (model.Viewer, error) {
	var v model.Viewer
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := scanLoginTx(tx.QueryRow(ctx, `SELECT `+loginTxColumns+` FROM login_transactions
			WHERE challenge = $1 AND mode IN ('popup', 'redirect') FOR UPDATE`, challenge))
		if errors.Is(err, ErrNotFound) {
			return ErrLoginPending
		}
		if err != nil {
			return err
		}
		if t.Status == "redeemed" || !t.ExpiresAt.After(time.Now()) {
			return ErrLoginGone
		}
		if t.SiteID == nil || *t.SiteID != siteID {
			// A verifier for another site is treated as unknown here.
			return ErrLoginPending
		}
		done := (t.Mode == ModePopup && t.Status == "authenticated") || (t.Mode == ModeRedirect && t.Status == "confirmed")
		if !done {
			return ErrLoginPending
		}
		if _, err := tx.Exec(ctx, `UPDATE login_transactions SET status = 'redeemed', redeemed_at = now() WHERE id = $1`, t.ID); err != nil {
			return err
		}
		var suspended bool
		if err := tx.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM users WHERE id = $1`, *t.UserID).Scan(&suspended); err != nil {
			return notFound(err)
		}
		if suspended {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO site_sessions (token_hash, user_id, site_id, idp_sid, created_via, client_label, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, in.TokenHash, *t.UserID, siteID, t.IDPSID, t.Mode, in.ClientLabel, in.ExpiresAt); err != nil {
			return err
		}
		if err := ensureSiteAuthor(ctx, tx, siteID, *t.UserID); err != nil {
			return err
		}
		v, err = viewer(ctx, tx, siteID, *t.UserID)
		return err
	})
	return v, err
}

func ensureSiteAuthor(ctx context.Context, q querier, siteID, userID int64) error {
	_, err := q.Exec(ctx, `INSERT INTO site_authors (site_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, siteID, userID)
	return err
}

func viewer(ctx context.Context, q querier, siteID, userID int64) (model.Viewer, error) {
	v := model.Viewer{UserID: userID}
	err := q.QueryRow(ctx, `SELECT sa.public_id::text, u.display_name, coalesce(m.role, ''), u.notify_replies,
		u.avatar_kind = 'idp' AND a.user_id IS NOT NULL, coalesce(a.etag, ''), u.email_enc IS NOT NULL, u.suspended_at IS NOT NULL
		FROM users u
		JOIN site_authors sa ON sa.user_id = u.id AND sa.site_id = $1
		LEFT JOIN site_members m ON m.user_id = u.id AND m.site_id = $1
		LEFT JOIN avatars a ON a.user_id = u.id
		WHERE u.id = $2`, siteID, userID).Scan(&v.AuthorID, &v.DisplayName, &v.Role, &v.NotifyReplies, &v.HasAvatar, &v.AvatarTag, &v.HasEmail, &v.Suspended)
	return v, notFound(err)
}

// SiteViewer resolves a site session token for the site it was issued
// for. Reading a session slides its expiry at most once a day, up to the
// maximum age.
func (s *Store) SiteViewer(ctx context.Context, tokenHash []byte, siteID int64) (model.Viewer, error) {
	var userID int64
	var created, lastUsed time.Time
	err := s.pool.QueryRow(ctx, `SELECT user_id, created_at, last_used_at FROM site_sessions
		WHERE token_hash = $1 AND site_id = $2 AND expires_at > now()`, tokenHash, siteID).Scan(&userID, &created, &lastUsed)
	if err != nil {
		return model.Viewer{}, notFound(err)
	}
	now := time.Now()
	if touchDue(lastUsed, now, policy.SessionTouchEvery) {
		expires := now.Add(policy.SiteSessionTTL)
		if limit := created.Add(policy.SiteSessionMaxAge); limit.Before(expires) {
			expires = limit
		}
		if _, err := s.pool.Exec(ctx, `UPDATE site_sessions SET last_used_at = now(), expires_at = $2 WHERE token_hash = $1`,
			tokenHash, expires); err != nil {
			return model.Viewer{}, err
		}
	}
	if err := ensureSiteAuthor(ctx, s.pool, siteID, userID); err != nil {
		return model.Viewer{}, err
	}
	return viewer(ctx, s.pool, siteID, userID)
}

// DeleteSiteSession ends one site session ("sign out of this site").
func (s *Store) DeleteSiteSession(ctx context.Context, tokenHash []byte, siteID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM site_sessions WHERE token_hash = $1 AND site_id = $2`, tokenHash, siteID)
	return err
}

// SiteSessionInfo describes a site session for the reader center.
type SiteSessionInfo struct {
	ID          string
	SiteID      string
	SiteName    string
	Origin      string
	ClientLabel string
	CreatedVia  string
	CreatedAt   time.Time
	LastUsedAt  time.Time
	ExpiresAt   time.Time
}

// SiteSessions lists a reader's live site sessions.
func (s *Store) SiteSessions(ctx context.Context, userID int64) ([]SiteSessionInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT ss.public_id::text, st.public_id::text, st.name,
		coalesce((SELECT o.origin FROM site_origins o WHERE o.site_id = st.id ORDER BY o.is_primary DESC, o.origin LIMIT 1), ''),
		ss.client_label, ss.created_via, ss.created_at, ss.last_used_at, ss.expires_at
		FROM site_sessions ss JOIN sites st ON st.id = ss.site_id
		WHERE ss.user_id = $1 AND ss.expires_at > now() ORDER BY ss.last_used_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SiteSessionInfo, error) {
		var i SiteSessionInfo
		err := r.Scan(&i.ID, &i.SiteID, &i.SiteName, &i.Origin, &i.ClientLabel, &i.CreatedVia, &i.CreatedAt, &i.LastUsedAt, &i.ExpiresAt)
		return i, err
	})
}

// DeleteSiteSessionByID ends one of a reader's site sessions.
func (s *Store) DeleteSiteSessionByID(ctx context.Context, userID int64, publicID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM site_sessions WHERE user_id = $1 AND public_id::text = $2`, userID, publicID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteAllSiteSessions signs a reader out of every site.
func (s *Store) DeleteAllSiteSessions(ctx context.Context, userID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM site_sessions WHERE user_id = $1`, userID)
	return tag.RowsAffected(), err
}

// CreateConsoleSession stores a first-party session and marks the console
// transaction that led to it redeemed.
func (s *Store) CreateConsoleSession(ctx context.Context, txID int64, tokenHash []byte, userID int64, sid string, expires time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE login_transactions SET status = 'redeemed', redeemed_at = now()
			WHERE id = $1 AND mode = 'console' AND status = 'authenticated'`, txID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrLoginGone
		}
		_, err = tx.Exec(ctx, `INSERT INTO console_sessions (token_hash, user_id, idp_sid, expires_at) VALUES ($1, $2, $3, $4)`,
			tokenHash, userID, sid, expires)
		return err
	})
}

// ConsoleUser resolves a console session.
func (s *Store) ConsoleUser(ctx context.Context, tokenHash []byte) (model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM console_sessions cs JOIN users u ON u.id = cs.user_id
		WHERE cs.token_hash = $1 AND cs.expires_at > now()`, tokenHash))
}

// DeleteConsoleSession ends a console session.
func (s *Store) DeleteConsoleSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM console_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteSessionsBySID ends every session issued during one identity
// service session (back-channel logout, login.md section 6).
func (s *Store) DeleteSessionsBySID(ctx context.Context, sid string) (int64, error) {
	if sid == "" {
		return 0, nil
	}
	var n int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := tx.Exec(ctx, `DELETE FROM site_sessions WHERE idp_sid = $1`, sid)
		if err != nil {
			return err
		}
		b, err := tx.Exec(ctx, `DELETE FROM console_sessions WHERE idp_sid = $1`, sid)
		n = a.RowsAffected() + b.RowsAffected()
		return err
	})
	return n, err
}

// DeleteUserSessions ends every session of an account.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	return deleteUserSessions(ctx, s.pool, userID)
}
