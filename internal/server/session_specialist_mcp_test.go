package server

import (
	"context"
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

func TestSessionSpecialistFrozenMCPMaximumRetainsLiveNarrowing(t *testing.T) {
	app, store, frame := newSessionSpecialistAPIFixture(t)
	for _, id := range []string{"captured-mcp", "later-mcp"} {
		if _, err := store.CreateMCPServer(workspace.MCPServerInput{ID: id, UserID: specialistFixtureOwner,
			Name: id, URL: "https://example.test/mcp", Transport: "streamable-http"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AttachAgentConnector(specialistFixtureOwner, "SYNTHETIC_A", "captured-mcp", "custom"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentConnectorToolExclusions(specialistFixtureOwner, "SYNTHETIC_A", "captured-mcp", []string{"captured_hidden"}); err != nil {
		t.Fatal(err)
	}
	prepared := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_A", "prepare-mcp-maximum", false)
	repo := app.transcriptStore
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:" + frame.ID,
		OwnerID: specialistFixtureOwner, ExternalID: frame.ID, SessionID: frame.ID, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), specialistInputFixture(prepared, stream, "mcp-authority-input")); err != nil {
		t.Fatal(err)
	}
	claim := specialistClaimFixture(t, repo, stream, "mcp-authority-runner")
	ctx := withTranscriptRunnerChatRun(context.Background(), &sessionRunnerChatRun{SessionID: frame.ID,
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claim}})
	snapshot, err := repo.ClaimedRunnerAgentSnapshot(t.Context(), claim)
	if err != nil || snapshot == nil {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if err := store.AttachAgentConnector(specialistFixtureOwner, "SYNTHETIC_A", "later-mcp", "custom"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAgentConnectorToolExclusions(specialistFixtureOwner, "SYNTHETIC_A", "captured-mcp", []string{"live_hidden"}); err != nil {
		t.Fatal(err)
	}
	current, found, err := app.workspaceMCPRuntimeContextWithContext(ctx, frame.ID)
	if err != nil || !found || len(current.Connectors) != 1 || current.Connectors[0].ID != "captured-mcp" {
		t.Fatalf("new attachment widened accepted maximum: %#v found=%t err=%v", current.Connectors, found, err)
	}
	if !current.workspaceMCPToolExcluded("captured-mcp", "captured_hidden") || !current.workspaceMCPToolExcluded("captured-mcp", "live_hidden") {
		t.Fatal("captured or live exclusion disappeared")
	}
	wrongSource := *snapshot
	wrongSource.Settings.MCPSourceKinds = map[string]string{"captured-mcp": "bundled"}
	if _, err := app.frozenSpecialistMCPRuntime(ctx, specialistFixtureOwner, frame.ID, wrongSource); !errors.Is(err, sessionagent.ErrTargetUnavailable) {
		t.Fatalf("same-ID source replacement accepted: %v", err)
	}
	disabled := false
	if _, err := store.UpdateMCPServer("captured-mcp", specialistFixtureOwner, workspace.UpdateMCPServerInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := app.workspaceMCPRuntimeTargetWithContext(ctx, frame.ID, "captured-mcp"); err != nil || found {
		t.Fatalf("disabled live connector remained callable: found=%t err=%v", found, err)
	}
	if err := store.DeleteMCPServer("captured-mcp", specialistFixtureOwner); err != nil {
		t.Fatal(err)
	}
	current, found, err = app.workspaceMCPRuntimeContextWithContext(ctx, frame.ID)
	if err != nil || !found || len(current.Connectors) != 0 {
		t.Fatalf("removed connector replaced by later attachment: %#v err=%v", current.Connectors, err)
	}
}
