package store

import (
	"bytes"
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreateClaim starts a claim of guest comments by email. verified is set
// for the account's own verified address, which needs no mail.
func (s *Store) CreateClaim(ctx context.Context, userID int64, emailHMAC, tokenHash []byte, expires time.Time, verified bool) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO email_claims (user_id, email_hmac, token_hash, expires_at, verified_at)
		VALUES ($1, $2, $3, $4, CASE WHEN $5 THEN now() END) RETURNING id`, userID, emailHMAC, tokenHash, expires, verified).Scan(&id)
	return id, err
}

// ClaimSummary counts claimable guest comments per site.
type ClaimSummary struct {
	Count int
	Sites []ClaimSite
}

// ClaimSite is one site's share of a claim.
type ClaimSite struct {
	PublicID string
	Name     string
	Count    int
}

// ClaimCounts counts the guest comments left with an address.
func (s *Store) ClaimCounts(ctx context.Context, emailHMAC []byte) (ClaimSummary, error) {
	return claimCounts(ctx, s.pool, emailHMAC)
}

func claimCounts(ctx context.Context, q querier, emailHMAC []byte) (ClaimSummary, error) {
	rows, err := q.Query(ctx, `SELECT st.public_id::text, st.name, count(*) FROM comments c JOIN sites st ON st.id = c.site_id
		WHERE c.author_kind = 'guest' AND c.author_email_hmac = $1 AND c.status <> 'deleted' AND st.status <> 'deleted'
		GROUP BY st.id ORDER BY st.name`, emailHMAC)
	if err != nil {
		return ClaimSummary{}, err
	}
	sites, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClaimSite, error) {
		var x ClaimSite
		err := r.Scan(&x.PublicID, &x.Name, &x.Count)
		return x, err
	})
	sum := ClaimSummary{Sites: sites}
	for _, x := range sites {
		sum.Count += x.Count
	}
	return sum, err
}

// VerifyClaim completes the mail step. Only the account that started the
// claim can, with the token from the mail; anyone else gets ErrNotFound.
func (s *Store) VerifyClaim(ctx context.Context, id, userID int64, tokenHash []byte) (ClaimSummary, error) {
	var sum ClaimSummary
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var hmac, stored []byte
		var verified *time.Time
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT email_hmac, token_hash, verified_at, expires_at FROM email_claims
			WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&hmac, &stored, &verified, &expires)
		if err != nil {
			return notFound(err)
		}
		if verified == nil {
			if !bytes.Equal(stored, tokenHash) || time.Now().After(expires) {
				return ErrNotFound
			}
			if _, err := tx.Exec(ctx, `UPDATE email_claims SET verified_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
		sum, err = claimCounts(ctx, tx, hmac)
		return err
	})
	return sum, err
}

// ConfirmClaim moves the verified address's guest comments to the account:
// they show its current name and avatar, the guest address is dropped and
// the old nickname goes to the moderation record (sites.md section 6). A
// claim is confirmed once and takes only comments written before it was
// verified, so a later guest comment with the address stays a guest's.
func (s *Store) ConfirmClaim(ctx context.Context, id, userID int64) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var hmac []byte
		var verified time.Time
		err := tx.QueryRow(ctx, `SELECT email_hmac, verified_at FROM email_claims WHERE id = $1 AND user_id = $2
			AND verified_at IS NOT NULL AND confirmed_at IS NULL FOR UPDATE`, id, userID).Scan(&hmac, &verified)
		if err != nil {
			return notFound(err)
		}
		rows, err := tx.Query(ctx, `SELECT c.id, c.site_id, coalesce(c.author_name, '') FROM comments c JOIN sites st ON st.id = c.site_id
			WHERE c.author_kind = 'guest' AND c.author_email_hmac = $1 AND c.status <> 'deleted' AND st.status <> 'deleted'
			AND c.created_at <= $2 FOR UPDATE OF c`, hmac, verified)
		if err != nil {
			return err
		}
		type claimed struct {
			id, site int64
			name     string
		}
		var list []claimed
		for rows.Next() {
			var x claimed
			if err := rows.Scan(&x.id, &x.site, &x.name); err != nil {
				rows.Close()
				return err
			}
			list = append(list, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, x := range list {
			if _, err := tx.Exec(ctx, `UPDATE comments SET author_kind = 'user', author_user_id = $2, author_name = NULL,
				author_email_enc = NULL, author_email_hmac = NULL, notify_email = false, updated_at = now() WHERE id = $1`, x.id, userID); err != nil {
				return err
			}
			if err := ensureSiteAuthor(ctx, tx, x.site, userID); err != nil {
				return err
			}
			if err := addEvent(ctx, tx, x.site, &x.id, Event{Action: "claim", Actor: &userID, Detail: map[string]any{"nickname": x.name}}); err != nil {
				return err
			}
		}
		n = len(list)
		_, err = tx.Exec(ctx, `UPDATE email_claims SET claimed_count = claimed_count + $2, confirmed_at = now() WHERE id = $1`, id, n)
		return err
	})
	return n, err
}
