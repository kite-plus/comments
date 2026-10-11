package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
)

const userColumns = `u.id, u.public_id::text, u.display_name, u.avatar_kind, coalesce(u.homepage_url, ''), u.locale,
	u.email_enc, u.email_hmac, u.notify_replies, u.on_account_delete, u.is_platform_admin, u.suspended_at,
	coalesce(u.suspend_reason, ''), u.created_at, u.updated_at, u.last_login_at,
	EXISTS (SELECT 1 FROM avatars a WHERE a.user_id = u.id), u.idp_suspended_at`

func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.PublicID, &u.DisplayName, &u.AvatarKind, &u.HomepageURL, &u.Locale,
		&u.EmailEnc, &u.EmailHMAC, &u.NotifyReplies, &u.OnAccountDelete, &u.IsPlatformAdmin, &u.SuspendedAt,
		&u.SuspendReason, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &u.HasAvatar, &u.IDPSuspendedAt)
	return u, notFound(err)
}

// OIDCLogin is what a verified ID token says about a reader.
type OIDCLogin struct {
	Issuer      string
	Subject     string
	DisplayName string // already chosen from name, preferred_username or a fallback
	Locale      string
	// EmailEnc and EmailHMAC are nil unless the identity service marked
	// the address verified.
	EmailEnc  []byte
	EmailHMAC []byte
}

// LoginUser finds the account for (issuer, subject), creating it on first
// login, and records the login. created reports a new account.
func (s *Store) LoginUser(ctx context.Context, in OIDCLogin) (u model.User, created bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `SELECT user_id FROM user_identities WHERE issuer = $1 AND subject = $2`,
			in.Issuer, in.Subject).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			created = true
			if err := tx.QueryRow(ctx, `INSERT INTO users (display_name, locale, email_enc, email_hmac, last_login_at)
				VALUES ($1, $2, $3, $4, now()) RETURNING id`,
				in.DisplayName, in.Locale, in.EmailEnc, in.EmailHMAC).Scan(&id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_identities (issuer, subject, user_id) VALUES ($1, $2, $3)`,
				in.Issuer, in.Subject, id); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// The display name is the reader's own after the first login;
			// the email follows the identity service, and goes when it is
			// no longer verified there, so claims never rest on an old one.
			if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at = now(), email_enc = $2, email_hmac = $3
				WHERE id = $1`, id, in.EmailEnc, in.EmailHMAC); err != nil {
				return err
			}
		}
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
		return err
	})
	return u, created, err
}

// User returns an account by internal ID.
func (s *Store) User(ctx context.Context, id int64) (model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.id = $1`, id))
}

// UserByPublicID returns an account by its public ID.
func (s *Store) UserByPublicID(ctx context.Context, publicID string) (model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u WHERE u.public_id::text = $1`, publicID))
}

// UserBySubject returns the account linked to (issuer, subject).
func (s *Store) UserBySubject(ctx context.Context, issuer, subject string) (model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users u
		JOIN user_identities i ON i.user_id = u.id WHERE i.issuer = $1 AND i.subject = $2`, issuer, subject))
}

// UsersByEmailHMAC returns the accounts whose verified email matches.
func (s *Store) UsersByEmailHMAC(ctx context.Context, hmac []byte) ([]model.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users u WHERE u.email_hmac = $1 ORDER BY u.id`, hmac)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.User, error) { return scanUser(r) })
}

// ProfileUpdate changes the fields that are set.
type ProfileUpdate struct {
	DisplayName     *string
	AvatarKind      *string
	HomepageURL     *string
	Locale          *string
	NotifyReplies   *bool
	OnAccountDelete *string
}

// UpdateProfile changes a reader's own settings.
func (s *Store) UpdateProfile(ctx context.Context, id int64, p ProfileUpdate) (model.User, error) {
	var homepage *string
	clearHomepage := false
	if p.HomepageURL != nil {
		homepage = nullString(*p.HomepageURL)
		clearHomepage = homepage == nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET
		display_name = coalesce($2, display_name),
		avatar_kind = coalesce($3, avatar_kind),
		homepage_url = CASE WHEN $8 THEN NULL ELSE coalesce($4, homepage_url) END,
		locale = coalesce($5, locale),
		notify_replies = coalesce($6, notify_replies),
		on_account_delete = coalesce($7, on_account_delete),
		updated_at = now()
		WHERE id = $1`, id, p.DisplayName, p.AvatarKind, homepage, p.Locale, p.NotifyReplies, p.OnAccountDelete, clearHomepage)
	if err != nil {
		return model.User{}, err
	}
	return s.User(ctx, id)
}

// SetAvatar stores the reader's resized avatar.
func (s *Store) SetAvatar(ctx context.Context, userID int64, contentType string, data []byte, etag string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO avatars (user_id, content_type, bytes, etag) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET content_type = $2, bytes = $3, etag = $4, fetched_at = now()`,
		userID, contentType, data, etag)
	return err
}

// Avatar is a stored avatar image.
type Avatar struct {
	ContentType string
	Bytes       []byte
	ETag        string
}

// AvatarByAuthor returns the avatar of the reader behind a site author ID,
// when the reader chose the identity service's picture.
func (s *Store) AvatarByAuthor(ctx context.Context, authorID string) (Avatar, error) {
	var a Avatar
	err := s.pool.QueryRow(ctx, `SELECT a.content_type, a.bytes, a.etag FROM site_authors sa
		JOIN users u ON u.id = sa.user_id JOIN avatars a ON a.user_id = u.id
		WHERE sa.public_id::text = $1 AND u.avatar_kind = 'idp'`, authorID).Scan(&a.ContentType, &a.Bytes, &a.ETag)
	return a, notFound(err)
}

// AvatarByUser returns a reader's stored avatar for the reader center.
func (s *Store) AvatarByUser(ctx context.Context, userID int64) (Avatar, error) {
	var a Avatar
	err := s.pool.QueryRow(ctx, `SELECT content_type, bytes, etag FROM avatars WHERE user_id = $1`, userID).
		Scan(&a.ContentType, &a.Bytes, &a.ETag)
	return a, notFound(err)
}

// SuspendUser is the platform's suspension: the account cannot post and
// all its sessions end.
func (s *Store) SuspendUser(ctx context.Context, id int64, reason string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET suspended_at = coalesce(suspended_at, now()), suspend_reason = $2, updated_at = now()
			WHERE id = $1`, id, nullString(reason))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return deleteUserSessions(ctx, tx, id)
	})
}

// UnsuspendUser lifts the platform's suspension only.
func (s *Store) UnsuspendUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET suspended_at = NULL, suspend_reason = NULL, updated_at = now() WHERE id = $1`, id)
	return err
}

// SetIDPSuspended records whether the identity service has the account
// deactivated, kept apart from the platform's suspension so reactivation
// never lifts that one. Suspending ends all sessions.
func (s *Store) SetIDPSuspended(ctx context.Context, id int64, suspended bool) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET idp_suspended_at = CASE WHEN $2 THEN coalesce(idp_suspended_at, now()) END,
			updated_at = now() WHERE id = $1`, id, suspended)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if suspended {
			return deleteUserSessions(ctx, tx, id)
		}
		return nil
	})
}

// SetPlatformAdmin grants or removes maintainer access.
func (s *Store) SetPlatformAdmin(ctx context.Context, id int64, admin bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET is_platform_admin = $2, updated_at = now() WHERE id = $1`, id, admin)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func deleteUserSessions(ctx context.Context, q querier, userID int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM site_sessions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM console_sessions WHERE user_id = $1`, userID)
	return err
}

// DeleteUserData removes a reader's data from this service, as
// docs/design/login.md section 7 describes: comments are rewritten first,
// then the user row goes and everything else cascades. mode is "remove" or
// "anonymize". With fromIDP false, a reader who is the only owner of a site
// gets ErrSoleOwner; with fromIDP true such sites are deleted instead,
// entering the recovery window. It returns the affected thread IDs.
func (s *Store) DeleteUserData(ctx context.Context, userID int64, mode string, fromIDP bool) error {
	if mode != "remove" && mode != "anonymize" {
		return ErrInvalid
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT m.site_id FROM site_members m JOIN sites s ON s.id = m.site_id
			WHERE m.user_id = $1 AND m.role = 'owner' AND s.status <> 'deleted'
			AND NOT EXISTS (SELECT 1 FROM site_members o WHERE o.site_id = m.site_id AND o.role = 'owner' AND o.user_id <> $1)`, userID)
		if err != nil {
			return err
		}
		sole, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(sole) > 0 {
			if !fromIDP {
				return ErrSoleOwner
			}
			if _, err := tx.Exec(ctx, `UPDATE sites SET status = 'deleted', deleted_at = now(), updated_at = now(),
				status_note = CASE WHEN suspended_at IS NOT NULL THEN status_note ELSE 'owner account removed' END
				WHERE id = ANY($1)`, sole); err != nil {
				return err
			}
		}
		rows, err = tx.Query(ctx, `UPDATE comments SET author_kind = 'former_user', author_user_id = NULL,
			author_name = NULL, author_url = NULL, updated_at = now(),
			status = CASE WHEN $2 = 'remove' THEN 'deleted' ELSE status END
			WHERE author_user_id = $1 RETURNING thread_id`, userID, mode)
		if err != nil {
			return err
		}
		threads, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if err := recountThreads(ctx, tx, threads); err != nil {
			return err
		}
		// Mail still queued for the account goes with it (moderation.md 6.3).
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE status = 'queued'
			AND (coalesce_key = 'reply:u:' || $1::bigint OR payload->>'user_id' = ($1::bigint)::text)`, userID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// recountThreads refreshes the published count of each thread.
func recountThreads(ctx context.Context, q querier, threadIDs []int64) error {
	if len(threadIDs) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE threads t SET
		comment_count = (SELECT count(*) FROM comments c WHERE c.thread_id = t.id AND c.status = 'published'),
		last_comment_at = (SELECT max(coalesce(c.published_at, c.created_at)) FROM comments c WHERE c.thread_id = t.id AND c.status = 'published'),
		updated_at = now()
		WHERE t.id = ANY($1)`, threadIDs)
	return err
}

// LocaleFromClaim maps an identity service locale to a supported one.
func LocaleFromClaim(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh") || locale == "" {
		return model.LocaleZH
	}
	return model.LocaleEN
}

// touchDue reports whether a session last touched at t should slide.
func touchDue(t, now time.Time, every time.Duration) bool { return now.Sub(t) >= every }
