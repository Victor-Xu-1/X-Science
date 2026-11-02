package runtimekv

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func runtimeWriterTestStore(t *testing.T) *Store {
	t.Helper()
	store := New(filepath.Join(t.TempDir(), "runtime-state.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Set("busy-test", "neighbor", "preserved"); err != nil {
		t.Fatal(err)
	}
	return store
}

func awaitRuntimeConnectionUse(t *testing.T, store *Store) {
	t.Helper()
	timer, tick := time.NewTimer(time.Second), time.NewTicker(time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
	for store.db.Stats().InUse == 0 {
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatal("writer call did not acquire its SQLite connection")
		}
	}
}

func assertRuntimeCancelledWriteAbsent(t *testing.T, store *Store) {
	t.Helper()
	if _, found, err := store.Get("busy-test", "cancelled"); err != nil || found {
		t.Fatalf("cancelled write appeared after lock release: found=%v err=%v", found, err)
	}
	if entry, found, err := store.Get("busy-test", "neighbor"); err != nil || !found || entry.Value != "preserved" || entry.Version != 1 {
		t.Fatalf("neighbor changed: %+v found=%v err=%v", entry, found, err)
	}
	if _, err := store.Set("busy-test", "after", "writable"); err != nil {
		t.Fatal(err)
	}
}

func TestSetContextSQLiteWriterExplicitCancel(t *testing.T) {
	store := runtimeWriterTestStore(t)
	release := holdRuntimeSQLiteWriter(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := store.SetContext(ctx, "busy-test", "cancelled", "must not commit")
		done <- err
	}()
	awaitRuntimeConnectionUse(t, store)
	started := time.Now()
	cancel()
	var err error
	late := false
	select {
	case err = <-done:
	case <-time.After(time.Second):
		late = true
		release()
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled call remained blocked after fixture writer release")
		}
	}
	release()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancellation error=%v", err)
	}
	assertRuntimeCancelledWriteAbsent(t, store)
	if late {
		t.Fatalf("explicit cancellation waited for writer release: %s", time.Since(started))
	}
}

func TestSetContextSQLiteWriterWaitsThenCommitsOnce(t *testing.T) {
	store := runtimeWriterTestStore(t)
	release := holdRuntimeSQLiteWriter(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		entry, err := store.SetContext(ctx, "busy-test", "waiting", "value")
		if err == nil && entry.Version != 1 {
			err = errors.New("one writer request incremented its version more than once")
		}
		done <- err
	}()
	awaitRuntimeConnectionUse(t, store)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	entry, found, err := store.Get("busy-test", "waiting")
	if err != nil || !found || entry.Version != 1 || entry.Value != "value" {
		t.Fatalf("committed entry=%+v found=%v err=%v", entry, found, err)
	}
}

func TestSetContextAlreadyCancelledDoesNotInitializeStore(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "not-created.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.SetContext(ctx, "busy-test", "cancelled", "value"); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-cancelled call error=%v", err)
	}
	if store.db != nil {
		t.Fatal("already-cancelled call initialized SQLite")
	}
	if _, err := os.Stat(store.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("already-cancelled call created a file: %v", err)
	}
	if _, err := store.Set("busy-test", "after", "writable"); err != nil {
		t.Fatal(err)
	}
}

func TestSetContextRestoresActualSQLiteBusyTimeout(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancelled"}[cancelled], func(t *testing.T) {
			store := runtimeWriterTestStore(t)
			if _, err := store.db.Exec(`PRAGMA busy_timeout=777`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			release := func() {}
			if cancelled {
				release = holdRuntimeSQLiteWriter(t, store)
			}
			_, err := store.SetContext(ctx, "busy-test", "cancelled", "value")
			release()
			if cancelled && !errors.Is(err, context.DeadlineExceeded) || !cancelled && err != nil {
				t.Fatalf("SetContext cancelled=%v error=%v", cancelled, err)
			}
			var timeout int
			if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 777 {
				t.Fatalf("original connection timeout not restored: %d err=%v", timeout, err)
			}
		})
	}
}

func TestSQLiteWriterContextRollsBackCancellationBeforeCommit(t *testing.T) {
	store := runtimeWriterTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.mu.Lock()
	err := store.withSQLiteWriterContext(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `INSERT INTO runtime_state_entries(namespace,entry_key,value_json,version,updated_at) VALUES('busy-test','cancelled','"value"',1,?)`, time.Now().UTC()); err != nil {
			return err
		}
		cancel() // The real transaction contains the row; cancellation must roll it back.
		return nil
	})
	store.mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transaction cancellation error=%v", err)
	}
	assertRuntimeCancelledWriteAbsent(t, store)
}

func TestSetBackgroundRetainsSQLiteBusyWaitAfterContextWrite(t *testing.T) {
	store := runtimeWriterTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := store.SetContext(ctx, "busy-test", "first", "value"); err != nil {
		t.Fatal(err)
	}
	release := holdRuntimeSQLiteWriter(t, store)
	done := make(chan error, 1)
	go func() {
		_, err := store.Set("busy-test", "background", "value")
		done <- err
	}()
	awaitRuntimeConnectionUse(t, store)
	select {
	case err := <-done:
		t.Fatalf("background write returned before fixture writer release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSetContextRetainsOriginalSQLiteBusyBudget(t *testing.T) {
	store := runtimeWriterTestStore(t)
	if _, err := store.db.Exec(`PRAGMA busy_timeout=35`); err != nil {
		t.Fatal(err)
	}
	release := holdRuntimeSQLiteWriter(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err := store.SetContext(ctx, "busy-test", "cancelled", "must not commit")
	elapsed := time.Since(started)
	release()
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code()&0xff != 5 || ctx.Err() != nil {
		t.Fatalf("expected original SQLite BUSY budget, error=%v context=%v", err, ctx.Err())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("context writer extended original35ms busy budget: %s", elapsed)
	}
	assertRuntimeCancelledWriteAbsent(t, store)
}

func TestSQLiteWriterContextDiscardsUnsettledConnection(t *testing.T) {
	store := runtimeWriterTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.mu.Lock()
	err := store.withSQLiteWriterContext(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `INSERT INTO runtime_state_entries(namespace,entry_key,value_json,version,updated_at) VALUES('busy-test','cancelled','"value"',1,?)`, time.Now().UTC()); err != nil {
			return err
		}
		// Exercise database/sql's real connection-discard boundary while the
		// transaction is still uncommitted; no fake driver or delayed writer.
		return conn.Raw(func(any) error { return driver.ErrBadConn })
	})
	store.mu.Unlock()
	if err == nil {
		t.Fatal("discarded connection was reported as a successful commit")
	}
	assertRuntimeCancelledWriteAbsent(t, store)
}
