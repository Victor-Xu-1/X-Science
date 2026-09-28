package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	runtimekv "synon-go/internal/persistence/runtimekv"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/toolgateway"
)

func replayExecutionFailures(projected []transcriptstore.RunnerReplayEvent, toolName string, arguments json.RawMessage) agentruntime.ExecutionFailureLedger {
	reducer := newDurableSemanticFailureReducer(toolName, arguments)
	for _, event := range projected {
		reducer.observe(event.Event, event.ResolvedPayloadJSON)
	}
	return reducer.state
}

func executionFailureBoundary(name string, arguments json.RawMessage, state agentruntime.ExecutionFailureLedger) map[string]any {
	return state.Boundary(agentruntime.ToolCall{Name: name, Arguments: arguments})
}

func replayFailureFixture(t *testing.T) (map[string]any, transcriptstore.RunnerReplayEvent) {
	t.Helper()
	input := map[string]any{"command": "python analysis.py", "environment": "analysis"}
	return input, semanticBudgetEvent(t, map[string]any{
		"toolName": "bash", "toolInput": input, "toolCapabilities": []string{"runtime-execution"},
		"toolPhase": "failed", "status": "failed",
		"toolResult": map[string]any{"ok": false, "code": "bash_nonzero_exit", "stderr": "ValueError: invalid input", "files_written": []any{}},
	})
}

func TestFailureReplayDistinguishesInspectionFromCommittedRepair(t *testing.T) {
	input, failure := replayFailureFixture(t)
	arguments, _ := json.Marshal(input)
	for _, test := range []struct {
		name        string
		result      map[string]any
		wantBlocked bool
	}{
		{"read-only inspection", map[string]any{"ok": true, "files_written": []any{}}, true},
		{"committed repair", map[string]any{"ok": true, "files_written": []any{"analysis.py"}}, false},
		{"failed repair with committed output", map[string]any{"ok": false, "files_written": []any{"analysis.py"}}, false},
		{"unchanged edit", map[string]any{"ok": true, "changed": false}, true},
		{"rejected call", map[string]any{"ok": false, "executed": false, "preflight": true, "files_written": []any{"analysis.py"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			phase := "completed"
			if test.result["ok"] == false {
				phase = "failed"
			}
			observed := semanticBudgetEvent(t, map[string]any{
				"toolName": "edit_file", "toolCapabilities": []string{"artifact-write"},
				"toolPhase": phase, "status": phase, "toolResult": test.result,
			})
			state := replayExecutionFailures([]transcriptstore.RunnerReplayEvent{failure, observed}, "bash", arguments)
			if got := executionFailureBoundary("bash", arguments, state) != nil; got != test.wantBlocked {
				t.Fatalf("blocked=%t want=%t state=%#v", got, test.wantBlocked, state)
			}
		})
	}
}

func TestFailureReplayNewUserTurnReopensExecutionButLeaseCheckpointDoesNot(t *testing.T) {
	input, failure := replayFailureFixture(t)
	arguments, _ := json.Marshal(input)
	for _, kind := range []string{"runner_checkpoint", "user_message"} {
		projected := []transcriptstore.RunnerReplayEvent{failure, {Event: transcriptstore.Event{Type: kind}}}
		state := replayExecutionFailures(projected, "bash", arguments)
		if got := executionFailureBoundary("bash", arguments, state) != nil; got != (kind == "runner_checkpoint") {
			t.Fatalf("event=%s state=%#v blocked=%t", kind, state, got)
		}
	}
}

func TestFailureReplayDoesNotLetAnotherFailedCallEraseExactFailure(t *testing.T) {
	input, first := replayFailureFixture(t)
	arguments, _ := json.Marshal(input)
	secondInput := map[string]any{"command": "python different.py", "environment": "analysis"}
	second := semanticBudgetEvent(t, map[string]any{
		"toolName": "bash", "toolInput": secondInput, "toolCapabilities": []string{"runtime-execution"},
		"toolPhase": "failed", "status": "failed", "toolResult": map[string]any{
			"ok": false, "code": "bash_nonzero_exit", "stderr": "ValueError: different input", "files_written": []any{},
		},
	})
	state := replayExecutionFailures([]transcriptstore.RunnerReplayEvent{first, second}, "bash", arguments)
	if executionFailureBoundary("bash", arguments, state) == nil {
		t.Fatalf("alternating failed calls reopened unchanged execution: %#v", state)
	}
}

func TestFailureReplayUsesSQLiteTranscriptInsteadOfStaleFailureCache(t *testing.T) {
	store, repository, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-repair", "frame-repair")
	ctx := context.Background()
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "stream-repair", OwnerID: "local", ExternalID: "frame-repair", SessionID: "frame-repair",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-repair", RootFrameID: "frame-repair", FrameID: "frame-repair", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "user-repair", FrameEventID: "user-event-repair",
		MessageUUID: "message-repair", Text: "Analyze the input",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner-repair", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	input, failure := replayFailureFixture(t)
	arguments, _ := json.Marshal(input)
	cache := runtimekv.New(filepath.Join(t.TempDir(), "legacy-cache.sqlite"))
	t.Cleanup(func() { _ = cache.Close() })
	if _, err := cache.Set("agent-runtime-semantic-failures", "old-failure", map[string]any{
		"sessionId": "frame-repair", "tool": "bash", "target": "legacy-target",
		"fingerprint": "bash|target|bash_nonzero_exit|failure", "executionIdentity": agentruntime.ExecutionCallFingerprint("bash", arguments), "executed": true,
	}); err != nil {
		t.Fatal(err)
	}
	appendCheckpoint := func(id string, payload []byte) {
		t.Helper()
		if _, _, _, err := repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
			Claim: claimed.Claim, ClientMessageID: id, Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendCheckpoint("failed-execution", failure.ResolvedPayloadJSON)
	newGateway := func() serverAgentRuntimeToolGateway {
		return serverAgentRuntimeToolGateway{server: &Server{workspaceStore: store, transcriptStore: repository, runtimeStore: cache}, sessionID: "frame-repair"}
	}
	if boundary, err := newGateway().durableSemanticFailureBoundary(ctx, "bash", input); err != nil || boundary == nil {
		t.Fatalf("fresh gateway forgot executed failure from SQLite: boundary=%#v err=%v", boundary, err)
	}
	appendCheckpoint("model-compact", []byte(`{"status":"completed","toolPhase":"auto_compact","summary":"model summary is not an execution receipt"}`))
	for index := 0; index < transcriptstore.MaxRunnerReplayProjection+5; index++ {
		appendCheckpoint(fmt.Sprintf("inspection-progress-%d", index), []byte(`{"status":"running","toolPhase":"progress"}`))
	}
	started := time.Now()
	if boundary, err := newGateway().durableSemanticFailureBoundary(ctx, "bash", input); err != nil || boundary == nil {
		t.Fatalf("pagination or model compaction erased failure: boundary=%#v err=%v", boundary, err)
	}
	t.Logf("fenced history scan across %d progress events: %s", transcriptstore.MaxRunnerReplayProjection+5, time.Since(started))
	// A live invocation already belongs to an admitted, unfinished batch.
	// Provider-message replay must reject it, but execution history must still
	// expose earlier committed receipts without waiting on the current call.
	root, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{
		map[string]any{"id": "current-call", "type": "function", "name": "bash", "arguments": input},
	}})
	var batch workspace.ToolCallBatch
	if _, _, _, err := repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimed.Claim, ClientMessageID: "current-model-batch", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: root,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var createErr error
			batch, _, _, createErr = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{
				BatchID: batch.BatchID, CallCount: batch.CallCount,
			}}, createErr
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimToolCallBatch(ctx, workspace.ClaimToolCallBatchInput{
		Claim: claimed.Claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListRunnerReplay(ctx, transcriptstore.ListRunnerReplayInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 10, CheckpointLimit: 10,
	}); !errors.Is(err, transcriptstore.ErrOpenToolBatch) {
		t.Fatalf("provider replay admitted the unfinished batch: %v", err)
	}
	if boundary, err := newGateway().durableSemanticFailureBoundary(ctx, "bash", input); err != nil || boundary == nil {
		t.Fatalf("live batch prevented reading an earlier failure: boundary=%#v err=%v", boundary, err)
	}
	if boundary, err := newGateway().durableSemanticFailureBoundary(ctx, "read_file", map[string]any{"file_path": "analysis.py"}); err != nil || boundary != nil {
		t.Fatalf("live batch blocked unrelated inspection: boundary=%#v err=%v", boundary, err)
	}
	appendCheckpoint("committed-repair", []byte(`{"toolName":"edit_file","toolCapabilities":["artifact-write"],"toolPhase":"completed","status":"completed","toolResult":{"ok":true,"changed":true}}`))
	if boundary, err := newGateway().durableSemanticFailureBoundary(ctx, "bash", input); err != nil || boundary != nil {
		t.Fatalf("stale failure cache overrode the committed repair in SQLite: boundary=%#v err=%v", boundary, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newGateway().durableSemanticFailureBoundary(canceled, "bash", input); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled authority read silently admitted execution: %v", err)
	}
}

func TestFailureReplayUnavailableHistoryKeepsSafeNonExecutedReceipt(t *testing.T) {
	gateway := serverAgentRuntimeToolGateway{
		server: &Server{transcriptStore: transcriptstore.NewRepository(nil)}, sessionID: "history-unavailable",
	}
	invocation := toolgateway.NewInvocation(context.Background(), "call-history", "read_file", nil,
		&serverAgentRuntimeGatewayExecution{gateway: gateway, call: agentruntime.ToolCall{ID: "call-history"}})
	invocation.CanonicalName = "read_file"
	invocation.Input = map[string]any{"file_path": "analysis.py"}
	serverAgentRuntimeGatewayFailureBudget(invocation)
	value, ok := invocation.Value.(map[string]any)
	if !ok || value["code"] != "execution_history_unavailable" || value["executed"] != false || value["preflight"] != true ||
		invocation.Err != nil || invocation.ContinuationState() != toolgateway.AuditOnly {
		t.Fatalf("unavailable history lost non-executed boundary: %#v", invocation)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), transcriptstore.ErrSchemaUnavailable.Error()) {
		t.Fatalf("persistence error leaked to tool result: %s", raw)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	invocation.Context = canceled
	serverAgentRuntimeGatewayFailureBudget(invocation)
	if !errors.Is(invocation.Err, context.Canceled) {
		t.Fatalf("cancellation was converted into retryable tool feedback: %v", invocation.Err)
	}
}
