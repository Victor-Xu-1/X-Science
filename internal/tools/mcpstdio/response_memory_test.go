package mcpstdio

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"synon-go/internal/runtimecontrol"
	"testing"
)

func TestMCPResponseDecodeHonorsRuntimeMemoryBudget(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// A soft Go heap limit is not an allocation fence. The transport must
	// reject materialization before JSON decoding oversubscribes that limit.
	previous := debug.SetMemoryLimit(1)
	defer debug.SetMemoryLimit(previous)
	_, err := decodeMCPResponseStream(context.Background(), "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"body":"`+strings.Repeat("x", 1<<20)+`"}}`))
	if !errors.Is(err, runtimecontrol.ErrInsufficientMemory) {
		t.Fatalf("expected memory admission failure, got %v", err)
	}
	assertMCPResponseSpoolsRemoved(t)
}
