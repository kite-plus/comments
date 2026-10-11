package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/policy"
)

// Mail is an outbox row. Payload names the rows to render; the worker
// renders at send time, so mail about a comment removed meanwhile is
// dropped.
type Mail struct {
	ID          int64
	Kind        string
	ToEnc       []byte
	CoalesceKey string
	Locale      string
	Payload     json.RawMessage
	Attempts    int
	CreatedAt   time.Time
}

// EnqueueMail queues a mail; one with a coalesce key goes out with that
// key's other mail.
func (s *Store) EnqueueMail(ctx context.Context, m Mail) error {
	return enqueueMail(ctx, s.pool, m)
}

func enqueueMail(ctx context.Context, q querier, m Mail) error {
	at := "now()"
	if m.CoalesceKey != "" {
		at = coalescedAt("$3::text")
	}
	_, err := q.Exec(ctx, `INSERT INTO outbox (kind, to_enc, coalesce_key, locale, payload, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, `+at+`)`, m.Kind, m.ToEnc, nullString(m.CoalesceKey), m.Locale, []byte(m.Payload))
	return err
}

// ClaimMail leases due mail, with every queued mail sharing a coalesce key
// with it, so one process sends each group once.
func (s *Store) ClaimMail(ctx context.Context, limit int, lease time.Duration) ([]Mail, error) {
	var out []Mail
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, coalesce(coalesce_key, '') FROM outbox WHERE status = 'queued' AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		var ids []int64
		var keys []string
		for rows.Next() {
			var id int64
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			if key != "" {
				keys = append(keys, key)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		rows, err = tx.Query(ctx, `UPDATE outbox SET next_attempt_at = now() + $3 * interval '1 second'
			WHERE status = 'queued' AND (id = ANY($1) OR coalesce_key = ANY($2))
			RETURNING id, kind, to_enc, coalesce(coalesce_key, ''), locale, payload, attempts, created_at`, ids, keys, seconds(lease))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Mail, error) {
			var m Mail
			err := r.Scan(&m.ID, &m.Kind, &m.ToEnc, &m.CoalesceKey, &m.Locale, &m.Payload, &m.Attempts, &m.CreatedAt)
			return m, err
		})
		return err
	})
	return out, err
}

// MailSent marks mail sent.
func (s *Store) MailSent(ctx context.Context, ids []int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET status = 'sent', sent_at = now(), last_error = NULL WHERE id = ANY($1)`, ids)
	return err
}

// MailDropped marks mail that has nothing left to say as sent without
// sending it.
func (s *Store) MailDropped(ctx context.Context, ids []int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET status = 'sent', sent_at = now(), last_error = 'dropped' WHERE id = ANY($1)`, ids)
	return err
}

// MailFailed schedules a retry with exponential backoff, giving up after
// policy.MailMaxAttempts.
func (s *Store) MailFailed(ctx context.Context, ids []int64, msg string) error {
	if len([]rune(msg)) > 500 {
		msg = string([]rune(msg)[:500])
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET attempts = attempts + 1, last_error = $2,
		status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'queued' END,
		next_attempt_at = now() + $4 * power(2, attempts) * interval '1 second'
		WHERE id = ANY($1)`, ids, msg, policy.MailMaxAttempts, seconds(policy.MailFirstRetry))
	return err
}

// NotificationsEmailed records that reply mail went out for comments.
func (s *Store) NotificationsEmailed(ctx context.Context, userID int64, commentIDs []int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET emailed_at = now() WHERE user_id = $1 AND comment_id = ANY($2)`, userID, commentIDs)
	return err
}

// OwnerDue is a site owner with comments to hear about.
type OwnerDue struct {
	SiteID   int64
	UserID   int64
	Locale   string
	EmailEnc []byte
	Since    time.Time
}

// OwnersDue lists owners of sites with the given notification setting who
// have new published or pending comments by others since their last mail.
// Instant mail waits out the coalesce window after the previous one.
func (s *Store) OwnersDue(ctx context.Context, mode string) ([]OwnerDue, error) {
	gap := policy.MailCoalesceWindow
	if mode == model.NotifyDaily {
		gap = 20 * time.Hour
	}
	rows, err := s.pool.Query(ctx, `SELECT m.site_id, m.user_id, u.locale, u.email_enc, coalesce(m.last_notified_at, m.created_at)
		FROM site_members m JOIN sites st ON st.id = m.site_id JOIN users u ON u.id = m.user_id
		WHERE m.role = 'owner' AND st.owner_notify = $1 AND st.status = 'active'
		AND u.email_enc IS NOT NULL AND u.suspended_at IS NULL AND u.idp_suspended_at IS NULL
		AND (m.last_notified_at IS NULL OR m.last_notified_at <= now() - $2 * interval '1 second')
		AND EXISTS (SELECT 1 FROM comments c WHERE c.site_id = m.site_id AND c.status IN ('published', 'pending')
			AND c.created_at > coalesce(m.last_notified_at, m.created_at) AND c.author_user_id IS DISTINCT FROM m.user_id)`,
		mode, seconds(gap))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OwnerDue, error) {
		var o OwnerDue
		err := r.Scan(&o.SiteID, &o.UserID, &o.Locale, &o.EmailEnc, &o.Since)
		return o, err
	})
}

// QueueOwnerMail queues an owner's mail about new comments and moves their
// last_notified_at to now, in one transaction.
func (s *Store) QueueOwnerMail(ctx context.Context, o OwnerDue, kind string) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM comments WHERE site_id = $1 AND status IN ('published', 'pending')
			AND created_at > $2 AND author_user_id IS DISTINCT FROM $3 ORDER BY created_at, id`, o.SiteID, o.Since, o.UserID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		n = len(ids)
		payload, _ := json.Marshal(map[string]any{"site_id": o.SiteID, "user_id": o.UserID, "comment_ids": ids})
		if err := enqueueMail(ctx, tx, Mail{Kind: kind, ToEnc: o.EmailEnc, Locale: o.Locale, Payload: payload}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE site_members SET last_notified_at = now() WHERE site_id = $1 AND user_id = $2`, o.SiteID, o.UserID)
		return err
	})
	return n, err
}

// OwnerContacts lists the owners of a site who can get mail.
func (s *Store) OwnerContacts(ctx context.Context, siteID int64) ([]OwnerDue, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.site_id, m.user_id, u.locale, u.email_enc, now() FROM site_members m JOIN users u ON u.id = m.user_id
		WHERE m.site_id = $1 AND m.role = 'owner' AND u.email_enc IS NOT NULL AND u.suspended_at IS NULL AND u.idp_suspended_at IS NULL`, siteID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OwnerDue, error) {
		var o OwnerDue
		err := r.Scan(&o.SiteID, &o.UserID, &o.Locale, &o.EmailEnc, &o.Since)
		return o, err
	})
}

// CommentsByID returns comments in the given order, skipping missing ones.
func (s *Store) CommentsByID(ctx context.Context, ids []int64) ([]model.Comment, error) {
	rows, err := s.pool.Query(ctx, withPlace(commentSelect+` WHERE c.id = ANY($1)`), ids)
	list, err := collectPlaced(rows, err, nil)
	if err != nil {
		return nil, err
	}
	byID := map[int64]model.Comment{}
	for _, c := range list {
		byID[c.ID] = c
	}
	var out []model.Comment
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}
