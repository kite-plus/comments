package store

import (
	"context"
	"time"

	"github.com/kite-plus/comments/internal/policy"
)

// SpendPow records a used proof of work; false means it was used before.
func (s *Store) SpendPow(ctx context.Context, id []byte, expires time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO pow_spent (challenge_id, expires_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, expires)
	return tag.RowsAffected() == 1, err
}

// PrepareGuestConfirm records a guest address that asked for reply mail.
// It returns true when a confirmation mail should go out now: the address
// is unconfirmed and had none in the last day (moderation.md section 6.1).
func (s *Store) PrepareGuestConfirm(ctx context.Context, emailHMAC, emailEnc, tokenHash []byte, locale string) (bool, error) {
	var send bool
	err := s.pool.QueryRow(ctx, `INSERT INTO guest_emails (email_hmac, email_enc, confirm_token_hash, confirm_sent_at, locale)
		VALUES ($1, $2, $3, now(), $4)
		ON CONFLICT (email_hmac) DO UPDATE SET
			locale = $4,
			confirm_token_hash = CASE WHEN guest_emails.confirmed_at IS NULL AND guest_emails.opted_out_at IS NULL
				AND (guest_emails.confirm_sent_at IS NULL OR guest_emails.confirm_sent_at < now() - $5 * interval '1 second')
				THEN $3 ELSE guest_emails.confirm_token_hash END,
			confirm_sent_at = CASE WHEN guest_emails.confirmed_at IS NULL AND guest_emails.opted_out_at IS NULL
				AND (guest_emails.confirm_sent_at IS NULL OR guest_emails.confirm_sent_at < now() - $5 * interval '1 second')
				THEN now() ELSE guest_emails.confirm_sent_at END
		RETURNING coalesce(confirm_token_hash = $3, false)`, emailHMAC, emailEnc, tokenHash, locale, seconds(policy.GuestConfirmsPerDay.Window)).Scan(&send)
	return send, err
}

// ConfirmGuestEmail confirms the address a confirmation token was sent to.
func (s *Store) ConfirmGuestEmail(ctx context.Context, tokenHash []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE guest_emails SET confirmed_at = coalesce(confirmed_at, now()), confirm_token_hash = NULL
		WHERE confirm_token_hash = $1 AND confirm_sent_at > now() - $2 * interval '1 second'`, tokenHash, seconds(policy.GuestConfirmTTL))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// GuestOptOut stops all mail to a guest address.
func (s *Store) GuestOptOut(ctx context.Context, emailHMAC []byte) error {
	_, err := s.pool.Exec(ctx, `UPDATE guest_emails SET opted_out_at = coalesce(opted_out_at, now()) WHERE email_hmac = $1`, emailHMAC)
	return err
}

// SetOwnerNotify changes a site's owner mail setting, for the one-click
// unsubscribe in owner mail.
func (s *Store) SetOwnerNotify(ctx context.Context, siteID int64, mode string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sites SET owner_notify = $2, updated_at = now() WHERE id = $1`, siteID, mode)
	return err
}
