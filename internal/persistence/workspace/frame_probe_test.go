package workspace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestLiveFrameProbeDoesNotWaitForExclusiveSQLiteConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "test", Status: "completed", ConversationType: "task"}); err != nil {
		t.Fatal(err)
	}
	// Let SQLite's independent exclusive connection acquire its lock without
	// unrelated idle workspace connections keeping shared WAL locks open.
	store.db.SetMaxIdleConns(0)
	store.readDB.SetMaxIdleConns(0)
	store.readDB.SetMaxOpenConns(1)
	store.readDB.SetMaxIdleConns(1)
	blocker, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(dbPath)+"?_pragma=locking_mode(EXCLUSIVE)")
	if err != nil {
		t.Fatal(err)
	}
	blocker.SetMaxIdleConns(0)
	conn, err := blocker.Conn(t.Context())
	if err != nil {
		_ = blocker.Close()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			_ = conn.Close()
			_ = blocker.Close()
		})
	}
	t.Cleanup(release)
	var journal string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("fixture journal mode = %q, %v, want WAL", journal, err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE frames SET name=name WHERE id='frame'"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	type result struct {
		found bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		found, err := store.ProbeFrameExists(ctx, "frame")
		done <- result{found, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("live frame lookup remained blocked by the Workspace SQLite lock")
		release()
		select {
		case got = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("frame lookup did not exit after fixture lock release")
		}
	}
	var sqliteErr interface{ Code() int }
	if got.found || (!errors.Is(got.err, context.DeadlineExceeded) &&
		(!errors.As(got.err, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY)) {
		t.Fatalf("blocked frame lookup = found %t, error %v", got.found, got.err)
	}
	release()
	if frame, found, err := store.GetFrame("frame"); err != nil || !found || frame.ID != "frame" {
		t.Fatalf("ordinary frame read failed after contention: found=%t err=%v", found, err)
	}
	var timeout int
	if err := store.readDB.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("ordinary reader busy_timeout = %d, %v, want 5000", timeout, err)
	}
	if _, err := store.readDB.ExecContext(t.Context(), "PRAGMA busy_timeout=321"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"frame": true, "missing": false} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		found, err := store.ProbeFrameExists(ctx, id)
		cancel()
		if err != nil || found != want {
			t.Fatalf("frame probe %s = %t, %v, want %t", id, found, err, want)
		}
	}
	if err := store.readDB.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 321 {
		t.Fatalf("borrowed reader busy_timeout = %d, %v, want 321", timeout, err)
	}
}
