package postgres

import (
	"context"
	"errors"
	"time"
)

// CountExpiredEvents is a dry-run count for a manually approved retention job.
func (s *Store) CountExpiredEvents(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM comment_events WHERE created_at < $1`, before).Scan(&count)
	return count, err
}

// PruneCommentEvents removes one bounded batch. Event IDs and sequences are
// never renumbered, so a client must re-synchronize if its checkpoint expires.
// The schema already has comment_events_created_at(created_at,id).
func (s *Store) PruneCommentEvents(ctx context.Context, before time.Time, limit int) (int64, error) {
	if before.After(time.Now()) {
		return 0, errors.New("retention cutoff cannot be in the future")
	}
	if limit < 1 || limit > 10000 {
		return 0, errors.New("retention batch must be 1..10000")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM comment_events
		WHERE id IN (
			SELECT id FROM comment_events WHERE created_at < $1
			ORDER BY created_at,id LIMIT $2
		)`, before, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
