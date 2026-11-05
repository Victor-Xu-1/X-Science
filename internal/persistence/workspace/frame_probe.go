package workspace

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
)

// ProbeFrameExists is an optional read that reports SQLite contention instead
// of waiting for the ordinary workspace reader's busy timeout.
func (s *Store) ProbeFrameExists(ctx context.Context, id string) (found bool, err error) {
	db := s.readDatabase()
	if db == nil {
		return false, errors.New("workspace store is closed")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	var previousTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&previousTimeout); err != nil {
		return false, err
	}
	// A cancelled query must still restore the borrowed connection's policy.
	// This PRAGMA is connection-local and does not need a database lock.
	defer func() {
		if _, restoreErr := conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout=%d", previousTimeout)); restoreErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("restore workspace reader busy timeout: %w", restoreErr))
		}
	}()
	// The driver can prepare the schema-dependent SELECT before installing its
	// context interrupt. Disable SQLite's own busy sleep before that call.
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return false, err
	}
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM frames WHERE id=?)", id).Scan(&found); err != nil {
		return false, fmt.Errorf("probe workspace frame: %w", err)
	}
	return found, nil
}
