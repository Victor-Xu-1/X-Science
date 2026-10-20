package server

import (
	"testing"

	"x-science/internal/memorytools"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestClaimedMemoryPermissionCannotBorrowQueuedOptIn(t *testing.T) {
	for _, mode := range []string{"off", ""} {
		t.Run("claimed-"+mode, func(t *testing.T) {
			server := openWorkspaceMemoryToolServer(t)
			repo := server.transcriptStore
			stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:frame-root", OwnerID: "user-1", ExternalID: "frame-root", SessionID: "frame-root", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-1", RootFrameID: "frame-root", FrameID: "frame-root", Epoch: 1})
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if mode != "" {
				config = map[string]any{"memoryMode": mode}
			}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", FrameEventID: "initial", MessageUUID: "initial", Text: "Review fixture", RuntimeConfig: config}); err != nil {
				t.Fatal(err)
			}
			ctx, _ := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", map[string]any{"append": []any{map[string]any{"text": "fixture"}}})
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "queued", FrameEventID: "queued", MessageUUID: "queued", Text: "Continue fixture", RuntimeConfig: map[string]any{"memoryMode": "on"}}); err != nil {
				t.Fatal(err)
			}
			_, err = server.resolveWorkspaceMemoryScope(ctx, "frame-root")
			if err == nil || err.Error() != memorytools.ErrMemoryUnavailable.Error() {
				t.Fatalf("queued opt-in granted current claim memory: %v", err)
			}
		})
	}
}

func TestClaimedMemoryPermissionHonorsFreshRootOptOut(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	if _, err := server.workspaceStore.SetFrameRuntimeMetadata("frame-root", workspace.FrameRuntimeMetadata{ContextData: map[string]any{"_original_input": map[string]any{"memory_mode": "on"}}}); err != nil {
		t.Fatal(err)
	}
	ctx, _ := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", map[string]any{"append": []any{map[string]any{"text": "fixture"}}})
	if _, err := server.workspaceStore.SetFrameRuntimeMetadata("frame-root", workspace.FrameRuntimeMetadata{ContextData: map[string]any{"_original_input": map[string]any{"memory_mode": "off"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.resolveWorkspaceMemoryScope(ctx, "frame-root"); err == nil || err.Error() != memorytools.ErrMemoryUnavailable.Error() {
		t.Fatalf("fresh root opt-out ignored: %v", err)
	}
}
