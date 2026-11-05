package runtimekv

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreGetContextHonorsCancellationWhileBusy(t *testing.T) {
	for _, boundary := range []string{"store_mutex", "connection_pool"} {
		t.Run(boundary, func(t *testing.T) {
			store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
			if _, err := store.Set("fixture", "key", "unchanged"); err != nil {
				t.Fatal(err)
			}
			if boundary == "store_mutex" {
				store.mu.Lock()
				defer store.mu.Unlock()
			} else {
				store.db.SetMaxOpenConns(1)
				connection, err := store.db.Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := store.GetContext(ctx, "fixture", "key")
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("busy read error = %v, want caller deadline", err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("GetContext ignored the active caller deadline")
			}
		})
	}
}

func TestStoreGetContextPreservesLegacyReadContract(t *testing.T) {
	store := runtimeStoreFixture(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	want, err := store.Set("fixture", "key", map[string]any{"value": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{t.Context(), nil} {
		got, found, err := store.GetContext(ctx, "fixture", "key")
		if err != nil || !found || got.Namespace != want.Namespace || got.Key != want.Key || got.Version != want.Version {
			t.Fatalf("context read changed the stored identity: %#v found=%t err=%v", got, found, err)
		}
		if _, found, err := store.GetContext(ctx, "fixture", "missing"); err != nil || found {
			t.Fatalf("missing context read changed its contract: found=%t err=%v", found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := store.GetContext(ctx, "fixture", "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled read error = %v", err)
	}
	if got, found, err := store.Get("fixture", "key"); err != nil || !found || got.Version != want.Version {
		t.Fatalf("legacy read changed: %#v found=%t err=%v", got, found, err)
	}
}
