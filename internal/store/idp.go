package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// IDPEventSeen reports whether an identity service event was handled.
func (s *Store) IDPEventSeen(ctx context.Context, id string) (bool, error) {
	var seen bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM idp_events WHERE event_id = $1)`, id).Scan(&seen)
	return seen, err
}

// RecordIDPEvent marks an event handled.
func (s *Store) RecordIDPEvent(ctx context.Context, id, kind string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO idp_events (event_id, kind) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, kind)
	return err
}

// IDPCursor returns the reconciliation cursor, "" before the first run.
func (s *Store) IDPCursor(ctx context.Context) (string, error) {
	var c string
	err := s.pool.QueryRow(ctx, `SELECT cursor FROM idp_sync WHERE id = 1`).Scan(&c)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return c, err
}

// SetIDPCursor stores the reconciliation cursor.
func (s *Store) SetIDPCursor(ctx context.Context, cursor string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO idp_sync (id, cursor, synced_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET cursor = $1, synced_at = now()`, cursor)
	return err
}
