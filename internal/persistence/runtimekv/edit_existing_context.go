package runtimekv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EditExistingContext atomically edits one existing entry. Missing keys and
// declined edits leave the store untouched. The callback must honor ctx and
// must not reenter the store while its writer is held.
func (s *Store) EditExistingContext(ctx context.Context, namespace, key string, edit func(Entry) (any, bool, error)) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	if ctx == nil || edit == nil {
		return errors.New("runtime entry edit requires a context and callback")
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.ensureDatabase(); err != nil {
		return err
	}
	return s.withSQLiteWriterContext(ctx, func(conn *sql.Conn) error {
		entry, err := scanEntry(conn.QueryRowContext(ctx, `SELECT namespace,entry_key,value_json,version,updated_at
			FROM runtime_state_entries WHERE namespace=? AND entry_key=?`, namespace, key))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read runtime entry for edit: %w", err)
		}
		value, changed, err := edit(entry)
		if err != nil || !changed {
			return err
		}
		raw, cloned, err := encodeValue(value)
		if err != nil {
			return err
		}
		scope := inferScope(namespace, key, cloned)
		_, err = conn.ExecContext(ctx,
			`UPDATE runtime_state_entries SET value_json=?,version=version+1,
			owner_user_id=?,project_id=?,root_frame_id=?,frame_id=?,updated_at=?
			WHERE namespace=? AND entry_key=?`,
			raw, scope.OwnerUserID, scope.ProjectID, scope.RootFrameID, scope.FrameID, s.now().UTC(), namespace, key)
		if err != nil {
			return fmt.Errorf("edit runtime entry: %w", err)
		}
		return nil
	})
}
