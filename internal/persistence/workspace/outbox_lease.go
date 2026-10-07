package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// RenewOutboxClaim never revives an expired claim, even if no successor has
// claimed it yet. Database time is the shared lease clock across processes.
func (s *Store) RenewOutboxClaim(ctx context.Context, eventID, token string, lease time.Duration) (time.Time, error) {
	if s == nil || s.db == nil || lease < time.Millisecond || lease > outboxMaxLease || strings.TrimSpace(token) == "" {
		return time.Time{}, errors.New("valid outbox claim and lease are required")
	}
	var expiresMillis int64
	err := s.db.QueryRowContext(ctx, `UPDATE workspace_outbox SET lease_expires_at_ms=`+sqliteNowMillis+`+?
		WHERE event_id=? AND status='inflight' AND claim_token=? AND lease_expires_at_ms>`+sqliteNowMillis+` RETURNING lease_expires_at_ms`,
		lease.Milliseconds(), eventID, token).Scan(&expiresMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrOutboxClaimLost
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(expiresMillis).UTC(), nil
}
