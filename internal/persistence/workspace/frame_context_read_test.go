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

func TestFrameContextReadsDoNotWaitForSQLitePrepare(t *testing.T) {
	for _, trace := range []bool{false, true} {
		name := "frame"
		if trace {
			name = "trace"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workspace.db")
			store, err := Open(path)
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
			store.db.SetMaxIdleConns(0)
			store.readDB.SetMaxIdleConns(0)
			store.readDB.SetMaxOpenConns(1)
			store.readDB.SetMaxIdleConns(1)
			blocker, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path)+"?_pragma=locking_mode(EXCLUSIVE)")
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
			if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), "UPDATE frames SET name=name WHERE id='frame'"); err != nil {
				t.Fatal(err)
			}
			pool := store.readDB
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			started := time.Now()
			go func() {
				if trace {
					_, err := store.GetFrameTraceSnapshotWithContext(ctx, "frame", nil)
					done <- err
				} else {
					_, _, err := store.GetFrameWithContext(ctx, "frame")
					done <- err
				}
			}()
			var readErr error
			select {
			case readErr = <-done:
			case <-time.After(500 * time.Millisecond):
				t.Error("context read remained inside SQLite prepare after its deadline")
				release()
				select {
				case readErr = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("context read did not settle after isolated lock release")
				}
			}
			var sqliteErr interface{ Code() int }
			if !errors.Is(readErr, context.DeadlineExceeded) &&
				(!errors.As(readErr, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY) {
				t.Fatalf("locked context read error=%v", readErr)
			}
			t.Logf("locked %s read settled after %s: %v", name, time.Since(started), readErr)
			release()
			var timeout int
			if err := pool.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
				t.Fatalf("ordinary busy_timeout=%d, %v, want5000", timeout, err)
			}
			if _, found, err := store.GetFrame("frame"); err != nil || !found {
				t.Fatalf("legacy frame read found=%t err=%v", found, err)
			}
			if snapshot, err := store.GetFrameTraceSnapshot("frame", nil); err != nil || snapshot.FrameCount != 1 {
				t.Fatalf("legacy trace frameCount=%d err=%v", snapshot.FrameCount, err)
			}
			if _, err := pool.ExecContext(t.Context(), "PRAGMA busy_timeout=321"); err != nil {
				t.Fatal(err)
			}
			if trace {
				if _, err := store.GetFrameTraceSnapshotWithContext(t.Context(), "frame", nil); err != nil {
					t.Fatal(err)
				}
			} else if _, found, err := store.GetFrameWithContext(t.Context(), "frame"); err != nil || !found {
				t.Fatalf("context frame found=%t err=%v", found, err)
			}
			if err := pool.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 321 {
				t.Fatalf("borrowed reader busy_timeout=%d, %v, want321", timeout, err)
			}
		})
	}
}
