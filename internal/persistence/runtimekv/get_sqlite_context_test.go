package runtimekv

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestStoreGetContextDoesNotWaitForSQLiteReadLock(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	if _, err := store.Set("fixture", "key", "preserved"); err != nil {
		t.Fatal(err)
	}
	// Rollback journal mode is isolated to this fixture: it lets an independent
	// SQLite writer exclude a real read without contending on the Store mutex.
	var journal string
	if err := store.db.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("fixture journal mode=%q, err=%v", journal, err)
	}
	blocker, err := sql.Open(sqliteDriver, runtimeSQLiteURL(store.path))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := blocker.Conn(t.Context())
	if err != nil {
		_ = blocker.Close()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
			_ = conn.Close()
			_ = blocker.Close()
		})
	}
	t.Cleanup(release)
	if _, err := conn.ExecContext(t.Context(), `BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	type result struct {
		found bool
		err   error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		_, found, err := store.GetContext(ctx, "fixture", "key")
		done <- result{found, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(250 * time.Millisecond):
		t.Error("context-aware read remained blocked by SQLite's busy timeout")
		release()
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("read did not join after releasing the isolated SQLite lock")
		}
	}
	t.Logf("25ms caller: elapsed=%s found=%t err=%v", time.Since(started), got.found, got.err)
	var coded interface{ Code() int }
	if got.found || (!errors.Is(got.err, context.DeadlineExceeded) &&
		(!errors.As(got.err, &coded) || coded.Code()&0xff != sqlite3.SQLITE_BUSY)) {
		t.Fatalf("blocked read = found %t, err %v", got.found, got.err)
	}
	release()
	var timeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("ordinary read policy changed: timeout=%d err=%v", timeout, err)
	}
	if entry, found, err := store.Get("fixture", "key"); err != nil || !found || entry.Value != "preserved" || entry.Version != 1 {
		t.Fatalf("legacy read did not recover unchanged: %+v found=%t err=%v", entry, found, err)
	}
}

func TestStoreGetContextRestoresConnectionPolicy(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	if _, err := store.Set("fixture", "key", "preserved"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`PRAGMA busy_timeout=321`); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"key", "missing"} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		_, found, err := store.GetContext(ctx, "fixture", key)
		cancel()
		if err != nil || found != (key == "key") {
			t.Fatalf("context read %s: found=%t err=%v", key, found, err)
		}
	}
	var timeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 321 {
		t.Fatalf("borrowed connection policy changed: timeout=%d err=%v", timeout, err)
	}
}

func TestSQLiteReaderContextDiscardsUnrestoredConnection(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	if _, err := store.Set("fixture", "key", "preserved"); err != nil {
		t.Fatal(err)
	}
	// Discard the actual borrowed connection before cleanup. Restoration must
	// report failure instead of returning an uncertain policy to the pool.
	err := store.withSQLiteReaderContext(t.Context(), func(conn *sql.Conn) error {
		return conn.Raw(func(any) error { return driver.ErrBadConn })
	})
	if err == nil {
		t.Fatal("unrestorable connection was reported as a healthy read")
	}
	if _, found, err := store.Get("fixture", "key"); err != nil || !found {
		t.Fatalf("replacement connection unusable: found=%t err=%v", found, err)
	}
	var timeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("replacement connection inherited temporary policy: timeout=%d err=%v", timeout, err)
	}
}
