package server

import (
	"testing"

	"x-science/internal/memorytools"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestClaimedMemoryDerivedQueueCannotGrantCurrentClaim(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	stream, err := server.transcriptStore.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:frame-root", OwnerID: "user-1", ExternalID: "frame-root", SessionID: "frame-root", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-1", RootFrameID: "frame-root", FrameID: "frame-root", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := server.transcriptStore.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", FrameEventID: "initial", MessageUUID: "initial", Text: "Review fixture", RuntimeConfig: map[string]any{"memoryMode": "off"}}); err != nil {
		t.Fatal(err)
	}
	ctx, call := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", map[string]any{"append": []any{map[string]any{"text": "must not be written"}}})
	if _, err := server.workspaceStore.SetFrameRuntimeMetadata("frame-root", workspace.FrameRuntimeMetadata{ContextData: map[string]any{"_original_input": map[string]any{"memory_mode": "on"}}}); err != nil {
		t.Fatal(err)
	}
	event, _, _, err := server.transcriptStore.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "derived-queued", FrameEventID: "derived-queued", MessageUUID: "derived-queued", Text: "Continue fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if len(event.PayloadJSON) == 0 {
		t.Fatal("missing canonical queued event")
	}
	if _, err := server.resolveWorkspaceMemoryScope(ctx, "frame-root"); err == nil || err.Error() != memorytools.ErrMemoryUnavailable.Error() {
		t.Fatalf("derived queue granted read/search: %v", err)
	}
	gateway := serverAgentRuntimeToolGateway{server: server, sessionID: "frame-root", allowedTools: []string{"write_memory"}}
	result, err := gateway.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok || value["error"] != memorytools.ErrMemoryUnavailable.Error() {
		t.Fatalf("derived queue granted write: %#v", result.Value)
	}
	if count, err := server.workspaceStore.CountMemoriesForUser(t.Context(), "user-1"); err != nil || count != 0 {
		t.Fatalf("unexpected write: %d %v", count, err)
	}
}
