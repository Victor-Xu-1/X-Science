package runtimekv

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

// SQLite's busy handler can sleep after sqlite3_interrupt. Acquire the writer
// without that handler, retrying only BEGIN (which has no payload side effects).
// The Store mutex and its single borrowed connection own this temporary policy.
func (s *Store) withSQLiteWriterContext(ctx context.Context, write func(*sql.Conn) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var previousTimeout int
	if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&previousTimeout); err != nil {
		return err
	}
	inTransaction := false
	timeoutRestored := false
	restoreTimeout := fmt.Sprintf("PRAGMA busy_timeout=%d", previousTimeout)
	defer func() {
		if inTransaction {
			if _, rollbackErr := conn.ExecContext(context.Background(), `ROLLBACK`); rollbackErr != nil {
				// SQLite may already have rolled back an interrupted statement.
				// Never return an uncertain transaction to the connection pool.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				err = errors.Join(err, fmt.Errorf("rollback runtime state: %w", rollbackErr))
				return
			}
		}
		if !timeoutRestored {
			if _, restoreErr := conn.ExecContext(context.Background(), restoreTimeout); restoreErr != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				err = errors.Join(err, fmt.Errorf("restore runtime busy timeout: %w", restoreErr))
			}
		}
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Duration(previousTimeout) * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// No busy sleep and no payload; a successful BEGIN is followed by a
		// context check before writing, even if cancellation raced admission.
		_, beginErr := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`)
		if beginErr == nil {
			inTransaction = true
			break
		}
		var coded interface{ Code() int }
		if !errors.As(beginErr, &coded) || coded.Code()&0xff != sqlite3.SQLITE_BUSY {
			return beginErr
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return beginErr
		}
		timer := time.NewTimer(min(5*time.Millisecond, remaining))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := write(conn); err != nil {
		return err
	}
	// Finish connection-policy cleanup before committing, so a restoration
	// failure cannot turn a known successful durable write into an error.
	if _, err := conn.ExecContext(context.Background(), restoreTimeout); err != nil {
		return fmt.Errorf("restore runtime busy timeout: %w", err)
	}
	timeoutRestored = true
	if err := ctx.Err(); err != nil {
		return err
	}
	// Once COMMIT starts, settle its actual result; neither payload nor commit
	// is retried or relabelled as cancelled after a known successful commit.
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err != nil {
		return err
	}
	inTransaction = false
	return nil
}
