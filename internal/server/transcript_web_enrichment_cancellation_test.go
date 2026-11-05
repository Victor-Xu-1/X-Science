package server

import (
	"context"
	"errors"
	"testing"
)

func TestTranscriptWebHistoryEnrichmentHonorsCancelledRequest(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	page := []map[string]any{{
		"id": "tool-enrichment-cancel", "type": "tool_call", "position": "left",
		"content": map[string]any{
			"call_id": "enrichment-cancel-call", "name": "bash", "status": "completed",
			"args": map[string]any{"command": "synthetic"}, "output": map[string]any{"exit_code": 0},
		},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Actual pool-wait cancellation is tested at the Workspace read pool by
	// TestConversationReadMetadataCancelsReadPoolWait. A held writer is no
	// longer a reason for this response-only enrichment to wait.
	if err := fixture.server.enrichTranscriptWebConversationMessages(ctx, transcriptWebServingFrameID,
		fixture.fence.BranchID, page); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled enrichment=%v, want context.Canceled", err)
	}
}
