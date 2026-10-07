package kernel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProviderTransferIdleCancelsOnlyUnprogressedControlAttempt(t *testing.T) {
	parent := context.Background()
	ctx, stop := providerTransferContext(parent, t.TempDir(), 60*time.Millisecond)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stalled transfer was not bounded")
	}
	if !errors.Is(context.Cause(ctx), errProviderTransferIdle) || parent.Err() != nil {
		t.Fatal("transfer observation cancelled its parent execution")
	}
}

func TestProviderTransferProgressDoesNotCreateATotalDurationLimit(t *testing.T) {
	stage := t.TempDir()
	ctx, stop := providerTransferContext(context.Background(), stage, 100*time.Millisecond)
	defer stop()
	deadline := time.Now().Add(350 * time.Millisecond)
	count := 0
	for time.Now().Before(deadline) {
		count++
		if err := os.WriteFile(filepath.Join(stage, ".transfer-progress.json"), []byte(fmt.Sprintf(`{"phase":"download","bytes":%d}`, count)), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("healthy advancing transfer hit a total-duration ceiling")
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("no-progress transfer did not pause after progress ended")
	}
}
