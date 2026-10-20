package server

import (
	"context"
	"encoding/json"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	"x-science/internal/memorytools"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	toolregistry "x-science/internal/tools/registry"
)

func memoryGuardBoundaryFixture(t *testing.T, legacy bool, current map[string]any) (*Server, context.Context, agentruntime.ToolCall) {
	t.Helper()
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "user-1", "project-1", "frame-root")
	if err := store.SetMemoryEnabled(t.Context(), "user-1", true); err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewStore(t.TempDir())
	if err := sessions.Upsert(sessionstore.Session{ID: "frame-root", Project: &sessionstore.Project{ID: "project-1"}, Orchestration: map[string]any{"frame_id": "frame-root"}}); err != nil {
		t.Fatal(err)
	}
	config := memoryconfig.Default()
	config.Enabled, config.PIClassifierEnabled = true, false
	server := &Server{workspaceStore: store, transcriptStore: repo, sessionStore: sessions, tools: toolregistry.Default(), memoryConfig: config}
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:frame-root", OwnerID: "user-1", ExternalID: "frame-root", SessionID: "frame-root", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-1", RootFrameID: "frame-root", FrameID: "frame-root", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		forceLegacyTranscriptFrameAuthority(t, db, stream)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "prior", FrameEventID: "prior", MessageUUID: "prior", Text: "Review fixture", RuntimeConfig: map[string]any{"memoryMode": "on"}}); err != nil {
		t.Fatal(err)
	}
	if current != nil {
		if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "current", FrameEventID: "current", MessageUUID: "current", Text: "Continue fixture", InputData: current}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, call := prepareClaimedWorkspaceMemoryTool(t, server, "frame-root", map[string]any{"append": []any{map[string]any{"text": "a durable fixture fact"}}})
	return server, ctx, call
}

func exerciseMemoryGuardOperation(t *testing.T, server *Server, ctx context.Context, call agentruntime.ToolCall, name string) map[string]any {
	t.Helper()
	call.Name = name
	switch name {
	case "read_memory":
		call.Arguments = json.RawMessage(`{"entity":"frame"}`)
	case "search_memory":
		call.Arguments = json.RawMessage(`{"query":"fixture"}`)
	}
	gateway := serverAgentRuntimeToolGateway{server: server, sessionID: "frame-root", allowedTools: []string{name}}
	result, err := gateway.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("unexpected %s result: %#v", name, result.Value)
	}
	return value
}

func TestLegacyTranscriptMemoryGuardPreservesExplicitAccess(t *testing.T) {
	for _, name := range []string{"read_memory", "search_memory", "write_memory"} {
		t.Run(name, func(t *testing.T) {
			server, ctx, call := memoryGuardBoundaryFixture(t, true, nil)
			value := exerciseMemoryGuardOperation(t, server, ctx, call, name)
			if value["error"] != nil {
				t.Fatalf("legacy explicit-on %s blocked: %#v", name, value)
			}
			if name == "write_memory" {
				if count, err := server.workspaceStore.CountMemoriesForUser(t.Context(), "user-1"); err != nil || count != 1 {
					t.Fatalf("legacy write not committed: %d %v", count, err)
				}
			}
		})
	}
}

func TestClaimedMemoryGuardHonorsInputDataOnlyOptOut(t *testing.T) {
	for _, name := range []string{"read_memory", "search_memory", "write_memory"} {
		t.Run(name, func(t *testing.T) {
			server, ctx, call := memoryGuardBoundaryFixture(t, false, map[string]any{"memory_mode": "off"})
			value := exerciseMemoryGuardOperation(t, server, ctx, call, name)
			if value["error"] != memorytools.ErrMemoryUnavailable.Error() {
				t.Fatalf("inputData-only off granted %s: %#v", name, value)
			}
			if count, err := server.workspaceStore.CountMemoriesForUser(t.Context(), "user-1"); err != nil || count != 0 {
				t.Fatalf("opt-out wrote memory: %d %v", count, err)
			}
		})
	}
}
