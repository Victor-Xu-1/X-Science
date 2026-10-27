// Package workspacefixture supplies isolated current-schema databases to tests
// that exercise consumers rather than schema creation or migration.
// Production startup and migration tests must continue to use workspace.Open.
package workspacefixture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

var currentSchema templateCache

type templateCache struct {
	once sync.Once
	data []byte
	err  error
}

// DatabasePath returns a private, disposable database path. Each caller still
// opens it through workspace.Open, retaining normal journal validation, runtime
// pragmas and blob reconciliation. Only immutable, empty schema bytes are shared;
// stores, connections, mutable files and caller data are never shared.
func DatabasePath(t testing.TB) string {
	t.Helper()
	return currentSchema.databasePath(t)
}

func (cache *templateCache) databasePath(t testing.TB) string {
	t.Helper()
	cache.once.Do(func() {
		// TempDir may stop the test on failure. Leave later callers a failure
		// rather than an empty template if initialization cannot finish.
		cache.err = errors.New("workspace template initialization did not finish")
		cache.data, cache.err = closedSchemaBytes(filepath.Join(t.TempDir(), "template.db"))
	})
	if cache.err != nil || len(cache.data) == 0 {
		t.Fatalf("initialize workspace test template: %v", cache.err)
	}
	path := filepath.Join(t.TempDir(), "workspace.db")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create isolated workspace test database: %v", err)
	}
	_, writeErr := file.Write(cache.data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatalf("copy workspace test template: %v", err)
	}
	return path
}

func closedSchemaBytes(path string) ([]byte, error) {
	store, err := workspace.Open(path)
	if err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, fmt.Errorf("close workspace template: %w", err)
	}
	// The last SQLite connection normally checkpoints WAL on close. Refuse
	// to copy a main file whose committed pages may still live in its WAL.
	if info, err := os.Stat(path + "-wal"); err == nil {
		if info.Size() != 0 {
			return nil, errors.New("workspace template still has a nonempty WAL after close")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect workspace template WAL: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workspace template: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("workspace template is empty")
	}
	return data, nil
}
