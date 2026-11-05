package runtimekv

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
)

// SQLite can prepare a read before installing its context interrupt. Borrow
// the Store's single connection without SQLite's busy sleep, then restore its
// ordinary policy before returning it. Reads never acquire a writer transaction.
func (s *Store) withSQLiteReaderContext(ctx context.Context, read func(*sql.Conn) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var previousTimeout int
	if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&previousTimeout); err != nil {
		return err
	}
	defer func() {
		if _, restoreErr := conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout=%d", previousTimeout)); restoreErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("restore runtime reader busy timeout: %w", restoreErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		return err
	}
	return read(conn)
}
