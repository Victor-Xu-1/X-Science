package runtimekv

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestLegacyNamespaceNoopDoesNotWaitForSQLiteWriter(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	if _, err := store.Set("fixture", "latest", "preserved"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open(sqliteDriver, runtimeSQLiteURL(store.path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _, _ = conn.ExecContext(context.Background(), `ROLLBACK`) }) }
	defer release()
	callback, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.EditNamespace("fixture", func(entries map[string]Entry) (bool, error) {
			close(callback)
			if entries["latest"].Value != "preserved" {
				t.Error("legacy no-op callback did not read current state")
			}
			return false, nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("legacy no-op edit under reserved writer: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("legacy no-op edit waited for an unrelated reserved writer")
		release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("legacy no-op edit did not settle after fixture release")
		}
	}
	select {
	case <-callback:
	default:
		t.Error("legacy no-op edit did not execute its callback")
	}
	release()
	entry, found, err := store.Get("fixture", "latest")
	if err != nil || !found || entry.Value != "preserved" || entry.Version != 1 {
		t.Fatalf("legacy no-op edit changed state: entry=%+v found=%v err=%v", entry, found, err)
	}
}

func TestEditNamespaceContextHonorsBusyAdmission(t *testing.T) {
	for _, boundary := range []string{"store_mutex", "connection_pool", "sqlite_writer"} {
		t.Run(boundary, func(t *testing.T) {
			store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
			if _, err := store.Set("busy-test", "neighbor", "preserved"); err != nil {
				t.Fatal(err)
			}
			before, err := store.List("busy-test")
			if err != nil {
				t.Fatal(err)
			}
			var release func()
			switch boundary {
			case "store_mutex":
				store.mu.Lock()
				release = store.mu.Unlock
			case "connection_pool":
				conn, err := store.db.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				release = func() { _ = conn.Close() }
			case "sqlite_writer":
				release = holdRuntimeSQLiteWriter(t, store)
			}
			var once sync.Once
			unlock := func() { once.Do(release) }
			t.Cleanup(unlock)
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- store.EditNamespaceContext(ctx, "busy-test", func(map[string]Entry) (bool, error) {
					t.Error("namespace callback ran without acquiring writer admission")
					return false, nil
				})
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("busy namespace edit = %v, want deadline", err)
				}
			case <-time.After(time.Second):
				t.Error("namespace edit ignored caller deadline")
				unlock()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("namespace edit did not exit after fixture release")
				}
			}
			unlock()
			after, err := store.List("busy-test")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("cancelled namespace edit changed state: before=%+v after=%+v err=%v", before, after, err)
			}
			if err := store.EditNamespace("busy-test", func(entries map[string]Entry) (bool, error) {
				entries["after"] = Entry{Value: "writable"}
				return true, nil
			}); err != nil {
				t.Fatalf("legacy edit failed after cancellation: %v", err)
			}
		})
	}
}

func TestEditNamespaceContextPreservesAtomicDetachedRewrite(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	if _, err := store.Set("fixture", "latest", map[string]any{"nested": map[string]any{"value": "original"}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.List("fixture")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("callback rejected")
	for _, outcome := range []string{"declined", "callback_error", "cancelled", "invalid_entry"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := store.EditNamespaceContext(ctx, "fixture", func(entries map[string]Entry) (bool, error) {
				entries["latest"].Value.(map[string]any)["nested"].(map[string]any)["value"] = "uncommitted"
				entries["history"] = Entry{Value: "uncommitted"}
				switch outcome {
				case "declined":
					return false, nil
				case "callback_error":
					return true, failure
				case "cancelled":
					cancel()
				case "invalid_entry":
					entries["invalid"] = Entry{Namespace: "another-namespace", Value: "invalid"}
				}
				return true, nil
			})
			if (outcome == "declined") != (err == nil) || outcome == "callback_error" && !errors.Is(err, failure) || outcome == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Errorf("namespace %s error = %v", outcome, err)
			}
			after, readErr := store.ListReadOnlyContext(t.Context(), "fixture")
			if readErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("namespace %s did not roll back: entries=%+v err=%v", outcome, after, readErr)
			}
		})
	}
	if err := store.EditNamespaceContext(t.Context(), "fixture", func(entries map[string]Entry) (bool, error) {
		entries["history"] = entries["latest"]
		entry := entries["history"]
		entry.Key = "history"
		entries["history"] = entry
		entries["latest"] = Entry{Value: "next"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List("fixture")
	if err != nil || len(entries) != 2 || entries[0].Key != "history" || entries[1].Key != "latest" || entries[1].Value != "next" {
		t.Fatalf("atomic namespace rewrite = %+v err=%v", entries, err)
	}
	entries[0].Value.(map[string]any)["nested"].(map[string]any)["value"] = "read-side mutation"
	entry, found, err := store.Get("fixture", "history")
	if err != nil || !found || entry.Value.(map[string]any)["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("read-side mutation reached durable namespace: entry=%+v found=%v err=%v", entry, found, err)
	}
}
