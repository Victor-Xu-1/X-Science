package workspace

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

// Borrow a request-owned connection using the same fail-fast SQLite policy as
// ProbeFrameExists. Legacy Background callers retain their ordinary busy wait.
func workspaceContextReadConnection(ctx context.Context, db *sql.DB) (*sql.Conn, func() error, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Done() == nil {
		return conn, conn.Close, nil
	}
	var previousTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&previousTimeout); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	release := func() error {
		_, restoreErr := conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout=%d", previousTimeout))
		if restoreErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
		if restoreErr != nil {
			return fmt.Errorf("restore workspace reader busy timeout: %w", restoreErr)
		}
		return nil
	}
	// modernc can prepare SQL before installing its context interrupt. Do not
	// enter SQLite's busy sleep while a cancellable caller owns this connection.
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return nil, nil, errors.Join(err, release())
	}
	return conn, release, nil
}
