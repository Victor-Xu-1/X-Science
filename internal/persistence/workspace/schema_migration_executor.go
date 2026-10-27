package workspace

import (
	"context"
	"database/sql"
	"fmt"
)

type schemaMigrationExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

var (
	_ schemaMigrationExecutor = (*sql.DB)(nil)
	_ schemaMigrationExecutor = (*sql.Conn)(nil)
)

// A borrowed transaction shares its caller's commit/rollback boundary. Legacy
// upgrade callers still create and commit the same owned SQL transaction.
func withSchemaMigrationTransaction(ctx context.Context, query schemaMigrationQueryExecutor, operation string, apply func(*sql.Tx) error) error {
	if tx, ok := query.(*sql.Tx); ok {
		return apply(tx)
	}
	owner, ok := query.(schemaMigrationExecutor)
	if !ok {
		return fmt.Errorf("schema migration transaction owner is unavailable")
	}
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		if operation != "" {
			return fmt.Errorf("begin %s: %w", operation, err)
		}
		return err
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if operation != "" {
			return fmt.Errorf("commit %s: %w", operation, err)
		}
		return err
	}
	return nil
}
