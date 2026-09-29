package runtimekv

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestVisitPrefixUsesLiteralIndexedScopeAndCancellation(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	defer store.Close()
	for _, key := range []string{"frame__1", "frame__2", "frame_x_1", "other"} {
		if _, err := store.Set("audit", key, map[string]any{"key": key}); err != nil {
			t.Fatal(err)
		}
	}
	var keys []string
	err := store.VisitPrefix(context.Background(), "audit", "frame__", func(e Entry) error { keys = append(keys, e.Key); return nil })
	if err != nil || !reflect.DeepEqual(keys, []string{"frame__1", "frame__2"}) {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.VisitPrefix(ctx, "audit", "frame", func(Entry) error { t.Fatal("cancelled read executed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	sentinel := errors.New("visitor stopped")
	if err := store.VisitPrefix(context.Background(), "audit", "frame", func(Entry) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := store.Set("audit", "after", true); err != nil {
		t.Fatal("visitor leaked lock:", err)
	}
}
