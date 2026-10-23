package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/toolcontract"
)

func TestRunnerHostGrantContextIncludesNewOrdinaryMessageAfterInterruption(t *testing.T) {
	f, client, request := hostGrantContextProjectionFixture(t)
	if _, _, err := f.store.CancelFrameWithTranscript(t.Context(), f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "new-ordinary-directory-input",
		FrameEventID: "new-ordinary-directory-event", MessageUUID: "new-ordinary-directory-message",
		Text: "Inspect the updated authorized directories.", MessageOrigin: "task_intent",
	}); err != nil || !created {
		t.Fatalf("new ordinary input created=%v err=%v", created, err)
	}
	if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateAutoResumeDispatch(f.stream.FrameID, f.stream.RootFrameID, f.stream.ProjectID, "OPERON", "new_composer_input"); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("directory-resume-owner", time.Minute)
	if err != nil || !found {
		t.Fatalf("dispatch found=%v err=%v", found, err)
	}
	var options SessionRunnerChatOptions
	if err := f.server.configureTranscriptFrameResume(t.Context(), dispatch, &options); err != nil {
		t.Fatal(err)
	}
	if options.TranscriptResumeSource != transcriptstore.ResumeSourceUserInput {
		t.Fatalf("actual dispatch source=%q; fixture must exercise new ordinary user-input recovery", options.TranscriptResumeSource)
	}
	claimed, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
		TTL: time.Minute, ResumeSource: options.TranscriptResumeSource, ResumeCheckpoint: options.TranscriptCheckpoint,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	input, err := f.repo.ClaimedRunnerInputContext(t.Context(), claimed.Claim)
	if err != nil || input == nil || input.Association == nil || input.Association.InputRevision != claimed.Claim.ClaimedInputRevision {
		t.Fatalf("ordinary admission=%+v err=%v", input, err)
	}
	client.run = &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: claimed.Claim}}
	prepared, err := client.prepare(t.Context(), request)
	if err != nil || len(prepared.Messages) != len(request.Messages)+1 || prepared.Messages[len(prepared.Messages)-1].Content != hostGrantContextChangedNotice {
		t.Fatalf("new ordinary message incorrectly treated as automatic continuation: extra=%d err=%v", len(prepared.Messages)-len(request.Messages), err)
	}
}

func TestRunnerHostGrantContextReusesActualExternalizedDiscovery(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	root := t.TempDir()
	for index := range 100 {
		path := filepath.Join(root, fmt.Sprintf("%03d-%s", index, strings.Repeat("directory-", 13)))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := f.server.upsertHostGrant(f.stream.OwnerID, path, "read"); err != nil {
			t.Fatal(err)
		}
	}
	const callID = "externalized-directory-discovery"
	ctx, run := appendLargeToolResultSource(t, f, callID, listHostGrantsToolName)
	value, err := f.server.executeAgentPermissionTool(ctx, f.identity,
		agentruntime.ToolCall{ID: callID, Name: listHostGrantsToolName}, listHostGrantsToolName, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if int64(len(raw)) <= runnerLargeToolResultInlineLimitBytes {
		t.Fatalf("fixture result=%d bytes; must cross the real inline limit", len(raw))
	}
	engine := f.server.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{SessionID: f.stream.SessionID, OutputLimitBytes: 256 << 10})
	materialized, err := engine.MaterializeToolResult(ctx, agentruntime.ToolCall{ID: callID, Name: listHostGrantsToolName}, value, agentruntime.ToolResultSucceeded)
	if err != nil || materialized.ResultRef == "" {
		t.Fatalf("normal externalization=%+v err=%v", materialized, err)
	}
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolCallID: callID, ToolName: listHostGrantsToolName, Result: string(materialized.JSON),
	}); err != nil {
		t.Fatal(err)
	}
	discovery, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || discovery.EventID == 0 {
		t.Fatalf("durable discovery=%+v err=%v", discovery, err)
	}
	request := agentruntime.ModelRequest{Tools: agentPermissionToolSchemas()[:1], Messages: []agentruntime.Message{
		{Role: "user", Content: "Use the directory discovery already retained."},
		{Role: "tool", ToolCallID: callID, Content: string(materialized.JSON)},
	}}
	client := runnerHostGrantContextClient{server: f.server, run: run, delegate: &hostGrantContextRecordingClient{}}
	prepared, err := client.prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range prepared.Messages[len(request.Messages):] {
		if strings.Contains(message.Content, "Call list_host_grants before") || strings.Contains(message.Content, "authorization has changed") {
			t.Fatalf("normal retained discovery prompted a redundant re-list: %q", message.Content)
		}
	}
	if len(prepared.Messages) != len(request.Messages)+1 || !strings.Contains(prepared.Messages[len(prepared.Messages)-1].Content, "retained") {
		t.Fatal("retained directory result lacks usable retrieval guidance")
	}
	var descriptor agentruntime.LargeToolResultDescriptor
	if err := json.Unmarshal(materialized.JSON, &descriptor); err != nil {
		t.Fatal(err)
	}
	guidance := prepared.Messages[len(prepared.Messages)-1].Content
	if !strings.Contains(guidance, toolcontract.CanonicalReadWith(descriptor.VersionID)) || strings.Contains(guidance, root) {
		t.Fatal("retained discovery lacks its exact safe reader or leaks grant rows into the notice")
	}
	// Compaction may remove the small descriptor as well. A retained result is
	// still read by its verified immutable version, not re-listed into another LTR.
	request.Messages = request.Messages[:1]
	prepared, err = client.prepare(t.Context(), request)
	if err != nil || !strings.Contains(prepared.Messages[len(prepared.Messages)-1].Content, toolcontract.CanonicalReadWith(descriptor.VersionID)) {
		t.Fatalf("compacted retained discovery lost its reader: %v", err)
	}
	if _, err := f.server.upsertHostGrant(f.stream.OwnerID, t.TempDir(), "read"); err != nil {
		t.Fatal(err)
	}
	prepared, err = client.prepare(t.Context(), request)
	if err != nil || prepared.Messages[len(prepared.Messages)-1].Content != hostGrantContextChangedNotice {
		t.Fatalf("retained historical snapshot hid a real authorization change: %v", err)
	}
	savedWorkspace := f.server.workspaceStore
	f.server.workspaceStore = nil
	defer func() { f.server.workspaceStore = savedWorkspace }()
	prepared, err = client.prepare(t.Context(), request)
	if err != nil || len(prepared.Messages) != len(request.Messages)+1 || strings.Contains(prepared.Messages[len(prepared.Messages)-1].Content, "Call list_host_grants before") {
		t.Fatalf("unavailable retained authority blocked the task or requested a redundant re-list: %v", err)
	}
	after, err := f.repo.ReadClaimedToolDiscovery(t.Context(), f.claim, listHostGrantsToolName)
	if err != nil || after.EventID != discovery.EventID || string(after.PayloadJSON) != string(discovery.PayloadJSON) {
		t.Fatal("retained discovery was rewritten or re-executed during context projection")
	}
}
