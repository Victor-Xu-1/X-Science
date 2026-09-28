package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestFailureConsistencyRetainsTypedFailureWithoutDiagnostic(t *testing.T) {
	args := json.RawMessage(`{"environment":"analysis","code":"run_job()"}`)
	reducer := newDurableSemanticFailureReducer("python", args)
	reducer.observe(transcriptstore.Event{Type: "runner_checkpoint"}, []byte(`{"toolName":"python","toolInput":{"environment":"analysis","code":"run_job()"},"toolCapabilities":["runtime-execution"],"toolPhase":"failed","status":"failed","toolResult":{"ok":false,"executed":true,"code":"runtime_draining"}}`))
	if got := executionFailureBoundary("python", args, reducer.state); got["code"] != "external_state_required" {
		t.Fatalf("typed failure was erased because prose was absent: %#v", got)
	}
}

func TestFailureConsistencyRetainsExternalFailureAfterDifferentFailure(t *testing.T) {
	args := json.RawMessage(`{"environment":"analysis","code":"run_job()"}`)
	reducer := newDurableSemanticFailureReducer("python", args)
	for _, payload := range []string{
		`{"toolName":"python","toolInput":{"environment":"analysis","code":"run_job()"},"toolPhase":"failed","status":"failed","toolResult":{"ok":false,"executed":true,"code":"runtime_draining","error":"runtime draining","files_written":[]}}`,
		`{"toolName":"python","toolInput":{"environment":"analysis","code":"inspect_job()"},"toolPhase":"failed","status":"failed","toolResult":{"ok":false,"executed":true,"code":"python_execution_failed","error":"invalid input","files_written":[]}}`,
	} {
		reducer.observe(transcriptstore.Event{Type: "runner_checkpoint"}, []byte(payload))
	}
	if got := executionFailureBoundary("python", args, reducer.state); got["code"] != "external_state_required" {
		t.Fatalf("another failure was treated as external recovery: %#v", got)
	}
}

func TestFailureConsistencyReplayedExternalFailureSurvivesUnrelatedMutation(t *testing.T) {
	arguments := json.RawMessage(`{"environment":"analysis","code":"run_job()"}`)
	reducer := newDurableSemanticFailureReducer("python", arguments)
	observe := func(payload string) {
		t.Helper()
		if !json.Valid([]byte(payload)) {
			t.Fatal("invalid diagnostic fixture")
		}
		reducer.observe(transcriptstore.Event{Type: "runner_checkpoint"}, []byte(payload))
	}
	observe(`{"toolName":"python","toolInput":{"environment":"analysis","code":"run_job()"},"toolCapabilities":["runtime-execution"],"toolPhase":"failed","status":"failed","toolResult":{"ok":false,"executed":true,"code":"runtime_draining","error":"runtime draining"}}`)
	before := executionFailureBoundary("python", arguments, reducer.state)
	if before["code"] != "external_state_required" {
		t.Fatalf("diagnostic setup did not reproduce external failure: state=%#v boundary=%#v", reducer.state, before)
	}
	observe(`{"toolName":"edit_file","toolInput":{"file_path":"notes.md","old_string":"old","new_string":"new"},"toolCapabilities":["artifact-write"],"toolPhase":"completed","status":"completed","toolResult":{"ok":true,"executed":true,"changed":true}}`)
	after := executionFailureBoundary("python", arguments, reducer.state)
	if after["code"] != "external_state_required" {
		t.Fatalf("inconsistent execution retry state: live guard blocks, replay admits after unrelated edit: state=%#v boundary=%#v", reducer.state, after)
	}
}

func TestFailureConsistencySQLiteExternalFailureSurvivesUnrelatedMutation(t *testing.T) {
	store, repository, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-chain-audit", "frame-chain-audit")
	ctx := context.Background()
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "stream-chain-audit", OwnerID: "local", ExternalID: "frame-chain-audit", SessionID: "frame-chain-audit",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-chain-audit", RootFrameID: "frame-chain-audit", FrameID: "frame-chain-audit", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "audit-user", FrameEventID: "audit-user-event",
		MessageUUID: "audit-message", Text: "Run the analysis",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "audit-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim failed: claimed=%t err=%v", claimed.Claimed, err)
	}
	appendCheckpoint := func(id, payload string) {
		t.Helper()
		if _, _, _, err := repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
			Claim: claimed.Claim, ClientMessageID: id, Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: []byte(payload),
		}); err != nil {
			t.Fatal(err)
		}
	}
	input := map[string]any{"environment": "analysis", "code": "run_job()"}
	newGateway := func() serverAgentRuntimeToolGateway {
		return serverAgentRuntimeToolGateway{server: &Server{workspaceStore: store, transcriptStore: repository}, sessionID: "frame-chain-audit"}
	}
	appendCheckpoint("audit-failure", `{"toolName":"python","toolInput":{"environment":"analysis","code":"run_job()"},"toolCapabilities":["runtime-execution"],"toolPhase":"failed","status":"failed","toolResult":{"ok":false,"executed":true,"code":"runtime_draining","error":"runtime draining"}}`)
	before, err := newGateway().durableSemanticFailureBoundary(ctx, "python", input)
	if err != nil || before["code"] != "external_state_required" {
		t.Fatalf("initial SQLite boundary=%#v err=%v", before, err)
	}
	appendCheckpoint("audit-unrelated-edit", `{"toolName":"edit_file","toolInput":{"file_path":"notes.md","old_string":"old","new_string":"new"},"toolCapabilities":["artifact-write"],"toolPhase":"completed","status":"completed","toolResult":{"ok":true,"executed":true,"changed":true}}`)
	after, err := newGateway().durableSemanticFailureBoundary(ctx, "python", input)
	if err != nil {
		t.Fatal(err)
	}
	if after["code"] != "external_state_required" {
		t.Fatalf("inconsistent execution retry state through real SQLite projection: before=%#v after=%#v", before, after)
	}
}
