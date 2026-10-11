package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/policy"
)

// Cleanup applies the retention periods of data-model.md section 4 and
// returns the number of rows touched per rule.
func (s *Store) Cleanup(ctx context.Context) (map[string]int64, error) {
	rules := []struct {
		name string
		sql  string
		arg  float64
	}{
		{"login_transactions", `DELETE FROM login_transactions WHERE expires_at < now() - $1 * interval '1 second'`, seconds(policy.TransactionsKeptAfterExpiry)},
		{"console_sessions", `DELETE FROM console_sessions WHERE expires_at < now() - $1 * interval '1 second'`, 0},
		{"site_sessions", `DELETE FROM site_sessions WHERE expires_at < now() - $1 * interval '1 second'`, 0},
		{"site_verifications", `DELETE FROM site_verifications WHERE expires_at < now() - $1 * interval '1 second'`, 0},
		{"pow_spent", `DELETE FROM pow_spent WHERE expires_at < now() - $1 * interval '1 second'`, 0},
		{"deleted_bodies", `UPDATE comments SET body_md = '', body_html = '', author_email_enc = NULL WHERE status = 'deleted'
			AND body_md <> '' AND updated_at < now() - $1 * interval '1 second'`, seconds(policy.DeletedBodyKept)},
		{"hidden_bodies", `UPDATE comments SET body_md = '', body_html = '' WHERE status IN ('hidden', 'spam')
			AND body_md <> '' AND updated_at < now() - $1 * interval '1 second'`, seconds(policy.HiddenBodyKept)},
		{"comment_revisions", `DELETE FROM comment_revisions WHERE revised_at < now() - $1 * interval '1 second'`, seconds(policy.RevisionsKept)},
		{"ip_hmac", `UPDATE comments SET ip_hmac = NULL WHERE ip_hmac IS NOT NULL AND created_at < now() - $1 * interval '1 second'`, seconds(policy.IPHashKept)},
		{"notifications", `DELETE FROM notifications WHERE created_at < now() - $1 * interval '1 second'`, seconds(policy.NotificationsKept)},
		{"sent_mail", `DELETE FROM outbox WHERE status = 'sent' AND sent_at < now() - $1 * interval '1 second'`, seconds(policy.SentMailKept)},
		{"failed_mail", `DELETE FROM outbox WHERE status = 'failed' AND created_at < now() - $1 * interval '1 second'`, seconds(policy.FailedMailKept)},
		{"reports", `DELETE FROM reports WHERE status <> 'open' AND resolved_at < now() - $1 * interval '1 second'`, seconds(policy.ResolvedReportsKept)},
		{"moderation_events", `DELETE FROM moderation_events WHERE created_at < now() - $1 * interval '1 second'`, seconds(policy.ModerationEventsKept)},
		{"guest_emails", `DELETE FROM guest_emails WHERE confirmed_at IS NULL AND opted_out_at IS NULL
			AND created_at < now() - $1 * interval '1 second'`, seconds(policy.UnconfirmedGuestEmailsKept)},
		{"open_claims", `DELETE FROM email_claims WHERE verified_at IS NULL AND expires_at < now() - $1 * interval '1 second'`, 0},
		{"done_claims", `DELETE FROM email_claims WHERE verified_at < now() - $1 * interval '1 second'`, seconds(policy.FinishedClaimsKept)},
		{"import_files", `UPDATE imports SET file = NULL WHERE file IS NOT NULL
			AND created_at < now() - $1 * interval '1 second'`, seconds(policy.ImportFilesKept)},
		{"idp_events", `DELETE FROM idp_events WHERE received_at < now() - $1 * interval '1 second'`, seconds(policy.IDPEventsKept)},
		{"deleted_sites", `DELETE FROM sites WHERE status = 'deleted' AND deleted_at < now() - $1 * interval '1 second'`, seconds(policy.DeletedSitesKept)},
		// Sites that never verified an origin, and aliases never verified,
		// do not outlive their token (sites.md section 2).
		{"unverified_sites", `DELETE FROM sites s WHERE s.status = 'unverified' AND s.verified_at IS NULL AND s.approved_at IS NULL
			AND s.created_at < now() - $1 * interval '1 second'
			AND NOT EXISTS (SELECT 1 FROM site_origins o WHERE o.site_id = s.id AND o.proof_hash IS NOT NULL)`, seconds(policy.UnverifiedOriginsKept)},
		{"unverified_origins", `DELETE FROM site_origins WHERE verified_at IS NULL AND proof_hash IS NULL AND NOT is_primary
			AND created_at < now() - $1 * interval '1 second'`, seconds(policy.UnverifiedOriginsKept)},
		{"expired_bans", `DELETE FROM bans WHERE expires_at < now() - $1 * interval '1 second'`, 0},
		{"unconfirmed_consents", `DELETE FROM guest_consents WHERE confirmed_at IS NULL
			AND created_at < now() - $1 * interval '1 second'`, seconds(policy.UnconfirmedConsentsKept)},
	}
	out := map[string]int64{}
	for _, r := range rules {
		tag, err := s.pool.Exec(ctx, r.sql, r.arg)
		if err != nil {
			return out, fmt.Errorf("cleanup %s: %w", r.name, err)
		}
		if n := tag.RowsAffected(); n > 0 {
			out[r.name] = n
		}
	}
	return out, nil
}

// Stale is a comment rendered by an older markup version.
type Stale struct {
	ID     int64
	BodyMD string
}

// StaleRenders returns comments whose render_version is below version.
func (s *Store) StaleRenders(ctx context.Context, version, limit int) ([]Stale, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, body_md FROM comments WHERE render_version < $1 ORDER BY id LIMIT $2`, version, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Stale, error) {
		var x Stale
		err := r.Scan(&x.ID, &x.BodyMD)
		return x, err
	})
}

// SetRender stores a comment's new rendering.
func (s *Store) SetRender(ctx context.Context, id int64, html string, version int) error {
	_, err := s.pool.Exec(ctx, `UPDATE comments SET body_html = $2, render_version = $3 WHERE id = $1`, id, html, version)
	return err
}
