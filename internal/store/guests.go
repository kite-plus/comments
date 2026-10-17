package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/policy"
)

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
