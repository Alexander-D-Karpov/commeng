package postgres

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// PurgeExpiredSessions deletes a bounded batch using the expires_at index.
// It does not run in the user-facing login path; multiple replicas may perform
// the cleanup safely because DELETE is transactional and idempotent.
func (s *Store) PurgeExpiredSessions(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 5000 {
		return 0, errors.New("session cleanup batch must be 1..5000")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash IN (
		SELECT token_hash FROM sessions WHERE expires_at <= now()
		ORDER BY expires_at LIMIT $1
	)`, batch)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RunSessionJanitor removes up to 20,000 stale sessions per pass to avoid a
// long-lived database lock. The next pass continues any remaining backlog.
func (s *Store) RunSessionJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		for i := 0; i < 20; i++ {
			operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			removed, err := s.PurgeExpiredSessions(operationCtx, 1000)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.WarnContext(ctx, "expired session cleanup failed", "error", err)
				}
				break
			}
			if removed < 1000 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
