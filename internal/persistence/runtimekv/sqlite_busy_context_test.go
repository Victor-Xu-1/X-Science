package runtimekv

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Keep a real second SQLite writer open; the Store mutex remains uncontended.
func holdRuntimeSQLiteWriter(t *testing.T, store *Store) func() {
	t.Helper()
	other, err := sql.Open(sqliteDriver, runtimeSQLiteURL(store.path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	other.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = other.Close() })
	tx, err := other.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE runtime_state_entries SET version=version WHERE namespace='busy-test' AND entry_key='neighbor'`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = tx.Rollback() }) }
	t.Cleanup(release)
	return release
}

func TestSetContextSQLiteWriterDeadline(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "runtime-state.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Set("busy-test", "neighbor", "preserved"); err != nil {
		t.Fatal(err)
	}
	release := holdRuntimeSQLiteWriter(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	type outcome struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		started := time.Now()
		_, err := store.SetContext(ctx, "busy-test", "cancelled", "must not commit")
		done <- outcome{err: err, elapsed: time.Since(started)}
	}()
	var result outcome
	late := false
	select {
	case result = <-done:
	case <-time.After(time.Second):
		late = true
		release() // A failed assertion must not leave a writer or blocked Store behind.
		select {
		case result = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("SetContext did not return after fixture writer was released")
		}
	}
	release()
	t.Logf("25ms context: elapsed=%s err=%v", result.elapsed, result.err)
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("SetContext error=%v, want deadline exceeded", result.err)
	}
	if _, found, err := store.Get("busy-test", "cancelled"); err != nil || found {
		t.Errorf("cancelled write committed after writer release: found=%v err=%v", found, err)
	}
	if entry, found, err := store.Get("busy-test", "neighbor"); err != nil || !found || entry.Value != "preserved" || entry.Version != 1 {
		t.Errorf("neighbor changed: entry=%+v found=%v err=%v", entry, found, err)
	}
	if _, err := store.Set("busy-test", "after", "writable"); err != nil {
		t.Errorf("Store unusable after cancellation: %v", err)
	}
	if late || result.elapsed > time.Second {
		t.Fatalf("SQLite writer busy wait ignored25ms context until fixture release: %s", result.elapsed)
	}
}
