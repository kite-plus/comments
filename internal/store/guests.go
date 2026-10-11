package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/policy"
)

// SpendPow records a used proof of work; false means it was used before.
func (s *Store) SpendPow(ctx context.Context, id []byte, expires time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO pow_spent (challenge_id, expires_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, expires)
	return tag.RowsAffected() == 1, err
}

// GuestConsent reports whether this browser (by the hash of the widget's
// guest token) confirmed reply mail for the address (moderation.md 6.1).
func (s *Store) GuestConsent(ctx context.Context, emailHMAC, browserHash []byte) (bool, error) {
	if browserHash == nil {
		return false, nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM guest_consents c JOIN guest_emails g ON g.email_hmac = c.email_hmac
		WHERE c.email_hmac = $1 AND c.browser_hash = $2 AND c.confirmed_at IS NOT NULL AND g.opted_out_at IS NULL)`,
		emailHMAC, browserHash).Scan(&ok)
	return ok, err
}

// RequestGuestConsent records that a guest asked for reply mail from a
// browser that has not confirmed it, for the given comment. It returns true
// when a confirmation mail should go out now: the address has not opted out
// and had none in the last day; otherwise nothing is recorded.
func (s *Store) RequestGuestConsent(ctx context.Context, emailHMAC, emailEnc, browserHash []byte, commentID int64, tokenHash []byte, locale string) (bool, error) {
	send := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var optedOut, recent bool
		err := tx.QueryRow(ctx, `INSERT INTO guest_emails (email_hmac, email_enc, locale) VALUES ($1, $2, $3)
			ON CONFLICT (email_hmac) DO UPDATE SET locale = $3
			RETURNING opted_out_at IS NOT NULL, coalesce(confirm_sent_at > now() - $4 * interval '1 second', false)`,
			emailHMAC, emailEnc, locale, seconds(policy.GuestConfirmsPerDay.Window)).Scan(&optedOut, &recent)
		if err != nil || optedOut || recent {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO guest_consents (email_hmac, browser_hash, comment_id, confirm_token_hash)
			VALUES ($1, $2, $3, $4)`, emailHMAC, browserHash, commentID, tokenHash); err != nil {
			return err
		}
		send = true
		_, err = tx.Exec(ctx, `UPDATE guest_emails SET confirm_sent_at = now() WHERE email_hmac = $1`, emailHMAC)
		return err
	})
	return send, err
}

// ConfirmGuestConsent confirms the consent a mail link was for: the
// address, the browser it came from, and reply mail for that comment.
func (s *Store) ConfirmGuestConsent(ctx context.Context, tokenHash []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var hmac []byte
		var commentID *int64
		err := tx.QueryRow(ctx, `UPDATE guest_consents SET confirmed_at = coalesce(confirmed_at, now())
			WHERE confirm_token_hash = $1 AND created_at > now() - $2 * interval '1 second'
			RETURNING email_hmac, comment_id`, tokenHash, seconds(policy.GuestConfirmTTL)).Scan(&hmac, &commentID)
		if err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE guest_emails SET confirmed_at = coalesce(confirmed_at, now()) WHERE email_hmac = $1`, hmac); err != nil {
			return err
		}
		if commentID != nil {
			_, err = tx.Exec(ctx, `UPDATE comments SET notify_email = true WHERE id = $1 AND author_email_hmac = $2`, *commentID, hmac)
		}
		return err
	})
}

// GuestOptOut stops all mail to a guest address and forgets its consents.
func (s *Store) GuestOptOut(ctx context.Context, emailHMAC []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE guest_emails SET opted_out_at = coalesce(opted_out_at, now()) WHERE email_hmac = $1`, emailHMAC); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET notify_email = false WHERE author_email_hmac = $1 AND notify_email`, emailHMAC); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM guest_consents WHERE email_hmac = $1`, emailHMAC)
		return err
	})
}

// SetOwnerNotify changes a site's owner mail setting, for the one-click
// unsubscribe in owner mail.
func (s *Store) SetOwnerNotify(ctx context.Context, siteID int64, mode string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sites SET owner_notify = $2, updated_at = now() WHERE id = $1`, siteID, mode)
	return err
}

// GuestMailable reports whether a guest address may get reply mail: it
// confirmed once and never opted out.
func (s *Store) GuestMailable(ctx context.Context, emailHMAC []byte) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM guest_emails WHERE email_hmac = $1
		AND confirmed_at IS NOT NULL AND opted_out_at IS NULL)`, emailHMAC).Scan(&ok)
	return ok, err
}
