package workspace

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestConversationReadMetadataCancelsReadPoolWait(t *testing.T) {
	store := newConversationReadPoolFixture(t)
	store.readDB.SetMaxOpenConns(1)
	for _, read := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"project", func(ctx context.Context) error {
			_, _, err := store.GetCompatibilityProjectWithContext(ctx, "owner", "project")
			return err
		}},
		{"trace", func(ctx context.Context) error {
			_, err := store.GetFrameTraceSnapshotWithContext(ctx, "frame", nil)
			return err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			reader, err := store.readDB.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			before := store.readDB.Stats().WaitCount
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- read.run(ctx) }()
			deadline := time.Now().Add(time.Second)
			for store.readDB.Stats().WaitCount == before {
				if time.Now().After(deadline) {
					_ = reader.Close()
					t.Fatal("read did not reach the occupied read pool")
				}
				runtime.Gosched()
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("read-pool cancellation=%v", err)
				}
			case <-time.After(time.Second):
				_ = reader.Close()
				t.Fatal("cancelled read remained behind the occupied read pool")
			}
		})
	}
}
