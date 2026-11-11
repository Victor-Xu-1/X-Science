// Package runtimekv owns bounded operational state that is not conversation
// history. It uses SQLite so one entry can be updated without rewriting every
// audit, approval, and runtime projection in the process.
package runtimekv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	now      func() time.Time
	initOnce sync.Once
	db       *sql.DB
	initErr  error
	closed   bool
}

// New opens a lazily initialized SQLite operational-state store. Production
// uses runtime-state.sqlite and never the retired whole-file JSON authority.
func New(path string) *Store {
	return &Store{path: strings.TrimSpace(path), now: time.Now}
}

func (s *Store) Set(namespace string, key string, value any) (Entry, error) {
	return s.SetContext(context.Background(), namespace, key, value)
}

func (s *Store) SetContext(ctx context.Context, namespace string, key string, value any) (Entry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateNamespace(namespace); err != nil {
		return Entry{}, err
	}
	if err := validateKey(key); err != nil {
		return Entry{}, err
	}
	if err := s.lockContext(ctx); err != nil {
		return Entry{}, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if err := s.ensureDatabase(); err != nil {
		return Entry{}, err
	}
	raw, cloned, err := encodeValue(value)
	if err != nil {
		return Entry{}, err
	}
	scope := inferScope(namespace, key, cloned)
	updatedAt := s.now().UTC()
	query := `
		INSERT INTO runtime_state_entries(
			namespace,entry_key,value_json,version,owner_user_id,project_id,root_frame_id,frame_id,updated_at
		) VALUES(?,?,?,1,?,?,?,?,?)
		ON CONFLICT(namespace,entry_key) DO UPDATE SET
			value_json=excluded.value_json,
			version=runtime_state_entries.version+1,
			owner_user_id=excluded.owner_user_id,
			project_id=excluded.project_id,
			root_frame_id=excluded.root_frame_id,
			frame_id=excluded.frame_id,
			updated_at=excluded.updated_at
		RETURNING version`
	args := []any{namespace, key, raw, scope.OwnerUserID, scope.ProjectID, scope.RootFrameID, scope.FrameID, updatedAt}
	var version int
	if ctx.Done() == nil {
		err = s.db.QueryRowContext(ctx, query, args...).Scan(&version)
	} else {
		err = s.withSQLiteWriterContext(ctx, func(conn *sql.Conn) error {
			return conn.QueryRowContext(ctx, query, args...).Scan(&version)
		})
	}
	if err != nil {
		return Entry{}, fmt.Errorf("set runtime state: %w", err)
	}
	return Entry{Namespace: namespace, Key: key, Value: cloned, Version: version, UpdatedAt: updatedAt}, nil
}

func (s *Store) lockContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.mu.TryLock() {
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// EditNamespace performs one atomic namespace edit. Values supplied to the
// callback are detached from SQLite, so changed=false discards nested edits.
func (s *Store) EditNamespace(namespace string, edit func(map[string]Entry) (bool, error)) error {
	return s.EditNamespaceContext(context.Background(), namespace, edit)
}

// EditNamespaceContext keeps the namespace rewrite atomic while honoring ctx
// at mutex, connection and SQLite writer admission. The callback must honor
// ctx and must not reenter the Store while its writer is held.
func (s *Store) EditNamespaceContext(ctx context.Context, namespace string, edit func(map[string]Entry) (bool, error)) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if edit == nil {
		return errors.New("runtime namespace edit callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.ensureDatabase(); err != nil {
		return err
	}
	if ctx.Done() == nil {
		// Preserve deferred admission for legacy callbacks that only inspect
		// the namespace. A reserved SQLite writer must not delay a no-op edit.
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin runtime namespace edit: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		changed, err := s.editNamespace(ctx, tx, namespace, edit)
		if err != nil || !changed {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit runtime namespace edit: %w", err)
		}
		return nil
	}
	return s.withSQLiteWriterContext(ctx, func(conn *sql.Conn) error {
		_, err := s.editNamespace(ctx, conn, namespace, edit)
		return err
	})
}

type runtimeNamespaceEditor interface {
	runtimeListQuerier
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) editNamespace(ctx context.Context, writer runtimeNamespaceEditor, namespace string, edit func(map[string]Entry) (bool, error)) (bool, error) {
	listed, err := listRuntimeEntries(ctx, writer, namespace)
	if err != nil {
		return false, err
	}
	entries := make(map[string]Entry, len(listed))
	for _, entry := range listed {
		entries[entry.Key] = entry
	}
	changed, err := edit(entries)
	if err != nil || !changed {
		return false, err
	}
	if _, err := writer.ExecContext(ctx, `DELETE FROM runtime_state_entries WHERE namespace=?`, namespace); err != nil {
		return false, fmt.Errorf("replace runtime namespace: %w", err)
	}
	for key, entry := range entries {
		if err := validateKey(key); err != nil {
			return false, err
		}
		if entry.Namespace != "" && entry.Namespace != namespace {
			return false, errors.New("runtime namespace edit changed entry namespace")
		}
		if entry.Key != "" && entry.Key != key {
			return false, errors.New("runtime namespace edit changed entry key")
		}
		if entry.Version <= 0 {
			entry.Version = 1
		}
		if entry.UpdatedAt.IsZero() {
			entry.UpdatedAt = s.now().UTC()
		}
		raw, cloned, err := encodeValue(entry.Value)
		if err != nil {
			return false, err
		}
		scope := inferScope(namespace, key, cloned)
		if _, err := writer.ExecContext(ctx, `INSERT INTO runtime_state_entries(
			namespace,entry_key,value_json,version,owner_user_id,project_id,root_frame_id,frame_id,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?)`, namespace, key, raw, entry.Version,
			scope.OwnerUserID, scope.ProjectID, scope.RootFrameID, scope.FrameID, entry.UpdatedAt.UTC()); err != nil {
			return false, fmt.Errorf("write runtime namespace entry: %w", err)
		}
	}
	return true, nil
}

func (s *Store) Get(namespace string, key string) (Entry, bool, error) {
	return s.GetContext(context.Background(), namespace, key)
}

func (s *Store) GetContext(ctx context.Context, namespace string, key string) (Entry, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateNamespace(namespace); err != nil {
		return Entry{}, false, err
	}
	if err := validateKey(key); err != nil {
		return Entry{}, false, err
	}
	if err := s.lockContext(ctx); err != nil {
		return Entry{}, false, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	if err := s.ensureDatabase(); err != nil {
		return Entry{}, false, err
	}
	const query = `SELECT namespace,entry_key,value_json,version,updated_at
		FROM runtime_state_entries WHERE namespace=? AND entry_key=?`
	var entry Entry
	var err error
	found := true
	if ctx.Done() == nil {
		entry, err = scanEntry(s.db.QueryRowContext(ctx, query, namespace, key))
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, false, nil
		}
	} else {
		err = s.withSQLiteReaderContext(ctx, func(conn *sql.Conn) error {
			var readErr error
			entry, readErr = scanEntry(conn.QueryRowContext(ctx, query, namespace, key))
			if errors.Is(readErr, sql.ErrNoRows) {
				found = false
				return nil
			}
			return readErr
		})
	}
	if err != nil {
		return Entry{}, false, err
	}
	return entry, found, nil
}

func (s *Store) List(namespace string) ([]Entry, error) {
	return s.ListReadOnlyContext(context.Background(), namespace)
}

// ListReadOnly keeps the historical API name. SQLite already returns detached
// values, so the safe read path is identical to List.
func (s *Store) ListReadOnly(namespace string) ([]Entry, error) {
	return s.List(namespace)
}

func (s *Store) Delete(namespace string, key string) (bool, error) {
	if err := validateNamespace(namespace); err != nil {
		return false, err
	}
	if err := validateKey(key); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDatabase(); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`DELETE FROM runtime_state_entries WHERE namespace=? AND entry_key=?`, namespace, key)
	if err != nil {
		return false, fmt.Errorf("delete runtime state: %w", err)
	}
	deleted, err := result.RowsAffected()
	return deleted == 1, err
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		s.closed = true
		return s.initErr
	}
	err := s.db.Close()
	s.db = nil
	s.closed = true
	return err
}
