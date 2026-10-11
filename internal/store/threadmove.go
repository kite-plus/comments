package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/kite-plus/comments/internal/model"
)

// MoveThread moves a thread's comments to another identifier, merging into
// the thread there if it exists (sites.md section 4). The moved comment IDs
// go into the moderation record, which UndoMove reads.
func (s *Store) MoveThread(ctx context.Context, siteID, fromID int64, toKey string, actor int64) (model.Thread, error) {
	var target int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		from, err := scanThread(tx.QueryRow(ctx, `SELECT `+threadColumns+` FROM threads WHERE site_id = $1 AND id = $2 FOR UPDATE`, siteID, fromID))
		if err != nil {
			return err
		}
		if from.PageKey == toKey {
			return ErrInvalid
		}
		created := false
		err = tx.QueryRow(ctx, `SELECT id FROM threads WHERE site_id = $1 AND page_key = $2 FOR UPDATE`, siteID, toKey).Scan(&target)
		if errors.Is(err, pgx.ErrNoRows) {
			created = true
			err = tx.QueryRow(ctx, `INSERT INTO threads (site_id, page_key, url, title, page_published_at, locked_at, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, siteID, toKey, from.URL, from.Title, from.PagePublishedAt,
				from.LockedAt, from.CreatedAt).Scan(&target)
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE comments SET thread_id = $2 WHERE thread_id = $1 RETURNING id`, fromID, target)
		if err != nil {
			return err
		}
		moved, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if err := recountThreads(ctx, tx, []int64{fromID, target}); err != nil {
			return err
		}
		return addEvent(ctx, tx, siteID, nil, Event{Action: "move_thread", Actor: &actor, Detail: map[string]any{
			"from_thread": fromID, "to_thread": target, "from_key": from.PageKey, "to_key": toKey,
			"comment_ids": moved, "created_target": created,
		}})
	})
	if err != nil {
		return model.Thread{}, err
	}
	return s.Thread(ctx, siteID, target)
}

// UndoMove moves the comments of a recorded move back.
func (s *Store) UndoMove(ctx context.Context, siteID, eventID, actor int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var raw []byte
		var action string
		if err := tx.QueryRow(ctx, `SELECT action, detail FROM moderation_events WHERE site_id = $1 AND id = $2 FOR UPDATE`,
			siteID, eventID).Scan(&action, &raw); err != nil {
			return notFound(err)
		}
		var d struct {
			From    int64   `json:"from_thread"`
			To      int64   `json:"to_thread"`
			IDs     []int64 `json:"comment_ids"`
			Created bool    `json:"created_target"`
			Undone  bool    `json:"undone"`
			UndoOf  int64   `json:"undo_of"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			return err
		}
		if action != "move_thread" || d.Undone || d.UndoOf != 0 {
			return ErrInvalid
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM threads WHERE id = $1 AND site_id = $2)`, d.From, siteID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET thread_id = $1 WHERE id = ANY($2) AND thread_id = $3`, d.From, d.IDs, d.To); err != nil {
			return err
		}
		if err := recountThreads(ctx, tx, []int64{d.From, d.To}); err != nil {
			return err
		}
		if d.Created {
			if _, err := tx.Exec(ctx, `DELETE FROM threads t WHERE t.id = $1 AND NOT EXISTS (SELECT 1 FROM comments c WHERE c.thread_id = t.id)`, d.To); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE moderation_events SET detail = detail || '{"undone": true}' WHERE id = $1`, eventID); err != nil {
			return err
		}
		return addEvent(ctx, tx, siteID, nil, Event{Action: "move_thread", Actor: &actor, Detail: map[string]any{
			"undo_of": eventID, "from_thread": d.To, "to_thread": d.From}})
	})
}
