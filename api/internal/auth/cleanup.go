package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// DeleteExpiredSessions deletes sessions whose expires_at is before now, batch rows per statement so
// no single DELETE holds many locks, and returns how many it deleted.
func (s *Store) DeleteExpiredSessions(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 { // the loop below would never end
		return 0, errors.New("auth: delete expired sessions: batch must be positive")
	}
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE ctid IN (
			SELECT ctid FROM sessions WHERE expires_at < $1 LIMIT $2)`, s.now(), batch)
		if err != nil {
			return total, fmt.Errorf("auth: delete expired sessions: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) {
			return total, nil
		}
	}
}

// CleanSessionsEvery deletes expired sessions every interval until ctx is done. Failures are logged
// and retried at the next tick.
func (s *Store) CleanSessionsEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.DeleteExpiredSessions(ctx, 1000); err != nil && ctx.Err() == nil {
				slog.Error("session cleanup", "err", err)
			} else if n > 0 {
				slog.Info("session cleanup", "deleted", n)
			}
		}
	}
}
