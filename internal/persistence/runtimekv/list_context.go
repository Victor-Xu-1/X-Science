package runtimekv

import (
	"context"
	"database/sql"
	"fmt"
)

// ListReadOnlyContext returns one detached, ordered namespace snapshot. An empty
// namespace lists all entries, matching List and the historical ListReadOnly API.
func (s *Store) ListReadOnlyContext(ctx context.Context, namespace string) ([]Entry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if namespace != "" {
		if err := validateNamespace(namespace); err != nil {
			return nil, err
		}
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureDatabase(); err != nil {
		return nil, err
	}
	if ctx.Done() == nil {
		return listRuntimeEntries(ctx, s.db, namespace)
	}
	var entries []Entry
	err := s.withSQLiteReaderContext(ctx, func(conn *sql.Conn) error {
		var readErr error
		entries, readErr = listRuntimeEntries(ctx, conn, namespace)
		return readErr
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

type runtimeListQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listRuntimeEntries(ctx context.Context, reader runtimeListQuerier, namespace string) ([]Entry, error) {
	query := `SELECT namespace,entry_key,value_json,version,updated_at FROM runtime_state_entries`
	args := []any{}
	if namespace != "" {
		query += ` WHERE namespace=?`
		args = append(args, namespace)
	}
	query += ` ORDER BY namespace,entry_key`
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runtime state: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
