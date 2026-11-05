package server

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscriptWebHistoryEnrichmentCancelsWriterWait(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	page := []map[string]any{{
		"id": "tool-enrichment-cancel", "type": "tool_call", "position": "left",
		"content": map[string]any{
			"call_id": "enrichment-cancel-call", "name": "bash", "status": "completed",
			"args": map[string]any{"command": "synthetic"}, "output": map[string]any{"exit_code": 0},
		},
	}}
	held, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	go func() {
		holderDone <- fixture.store.WithTransaction(t.Context(), func(*sql.Tx) error {
			close(held)
			<-release // Occupy the writer connection without any SQL mutation or file lock.
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-holderDone:
		t.Fatalf("hold isolated writer: %v", err)
	case <-time.After(2 * time.Second):
		releaseWriter()
		t.Fatal("isolated writer barrier did not start")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	returned := false
	defer func() {
		releaseWriter()
		select {
		case err := <-holderDone:
			if err != nil {
				t.Errorf("release isolated writer: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("isolated writer holder did not finish")
		}
		if !returned {
			select {
			case err := <-result:
				t.Logf("enrichment returned only after writer release: %v", err)
			case <-time.After(2 * time.Second):
				t.Error("enrichment did not finish after writer release")
			}
		}
	}()
	go func() {
		result <- fixture.server.enrichTranscriptWebConversationMessages(ctx, transcriptWebServingFrameID,
			fixture.fence.BranchID, page)
	}()
	// Observe the real production trace read before cancelling; merely cancelling
	// a goroutine before it starts would not prove a connection-wait regression.
	deadline := time.Now().Add(2 * time.Second)
	buffer := make([]byte, 64<<10)
	for {
		stack := string(buffer[:runtime.Stack(buffer, true)])
		if strings.Contains(stack, "workspace.(*Store).GetFrameTraceSnapshot") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("enrichment did not reach the held production trace read")
		}
		runtime.Gosched()
	}
	cancel()
	select {
	case err := <-result:
		returned = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled enrichment returned %v, want context.Canceled", err)
		}
		t.Log("caller cancellation settled enrichment before writer release")
	case <-time.After(time.Second):
		t.Error("caller cancellation did not release the production trace connection wait")
	}
}
