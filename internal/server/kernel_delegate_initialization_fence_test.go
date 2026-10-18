package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestKernelDelegateInitializationFailureReplayPreservesNewInput(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	const outerID, callID, model = "delegate-initialization-cell", "delegate-initialization-call", "initialization-model"
	startDelegateInitializationBatch(t, f, outerID)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	configureKernelHostProvider(t, f.server, f.identity.access, "initialization-provider", "openai", provider.URL+"/v1", model, "initialization-secret", "synthetic-key")
	requests := []workspace.KernelDelegateRequest{{Task: "Original delegated input", Model: model}}
	admission, err := f.store.CaptureKernelDelegationAdmission(ctx, f.stream.FrameID, f.stream.OwnerID, callID, outerID, f.claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	children, err := f.store.CreateKernelSupervisedChildren(ctx, workspace.CreateKernelDelegatesInput{
		Admission: admission, ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID,
		ToolUseID: callID, Requests: requests,
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("accepted delegation children=%#v err=%v", children, err)
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(ctx, child)
	if err != nil || initial == nil || initial.InputRevision != 1 {
		t.Fatalf("initial target=%#v err=%v", initial, err)
	}
	if _, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: initial.StreamUID, OwnerID: child.OwnerUserID, ClientMessageID: "new-child-input",
		FrameEventID: "new-child-input-event", MessageUUID: "new-child-input-message", Text: "New explicit input owns the child now",
	}); err != nil || !created {
		t.Fatalf("new input created=%t err=%v", created, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: initial.StreamUID, OwnerID: child.OwnerUserID, RunnerID: "new-child-owner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision != 2 {
		t.Fatalf("new claim=%#v err=%v", claimed, err)
	}
	beforeParent, _, err := f.store.GetFrame(f.stream.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	beforeChild, _, err := f.store.GetFrame(child.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := f.store.ListFrameEvents(child.FrameID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	// A real Session-store read failure after an accepted delegation. Replaying
	// the same host call must not let its old initialization failure settle a
	// different canonical input/claim. Only this temporary fixture is damaged.
	index := filepath.Join(f.server.fileRoot, "sessions", "index.json")
	if err := os.MkdirAll(filepath.Dir(index), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("{invalid session index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.server.ensureKernelChildSession(child); err == nil {
		t.Fatal("fixture did not reach the real initialization error")
	}
	bound := kernelHostExecutionIdentity{access: f.identity.access, transcriptExecution: kernelTranscriptExecutionBinding{
		stream: f.stream, claim: f.claim, outerToolUseID: outerID, kernelID: "initialization-kernel", kernelGeneration: 1,
		currentKernelGeneration: func() uint64 { return 1 },
	}}
	result, err := f.server.handleKernelSupervisionHostCall(ctx, bound, f.identity.access, "host.delegate",
		[]any{[]any{map[string]any{"task": requests[0].Task, "model": model}}, map[string]any{"wait": false}}, nil, callID, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := result.([]any)
	if !ok || len(rows) != 1 || rows[0].(map[string]any)["frame_id"] != child.FrameID {
		t.Fatalf("actual delegation did not replay the original child: %#v", result)
	}
	response := rows[0].(map[string]any)
	failure, _ := response["error"].(string)
	if response["status"] != "failed" || response["dispatched"] != false || failure == "" {
		t.Errorf("failed replay claimed successful dispatch: %#v", response)
	}
	afterChild, _, err := f.store.GetFrame(child.FrameID)
	if err != nil || !reflect.DeepEqual(beforeChild, afterChild) {
		t.Errorf("old initialization failure changed current child: before=%#v after=%#v err=%v", beforeChild, afterChild, err)
	}
	afterSummary, found, err := f.store.GetKernelSupervisedChild(ctx, child.ParentFrameID, child.FrameID, child.OwnerUserID)
	if err != nil || !found || !reflect.DeepEqual(child, afterSummary) {
		t.Errorf("old initialization failure replaced newer summary: before=%#v after=%#v err=%v", child, afterSummary, err)
	}
	afterEvents, err := f.store.ListFrameEvents(child.FrameID, 0, 100)
	if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
		t.Errorf("old initialization failure published new child events: before=%#v after=%#v err=%v", beforeEvents, afterEvents, err)
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(ctx, claimed.Claim); err != nil {
		t.Errorf("new canonical owner was changed: %v", err)
	}
	afterParent, _, err := f.store.GetFrame(f.stream.FrameID)
	if err != nil || !reflect.DeepEqual(beforeParent, afterParent) {
		t.Errorf("parent changed: before=%#v after=%#v err=%v", beforeParent, afterParent, err)
	}
	stats, err := f.store.KernelDelegationStats(ctx, child.ParentFrameID, child.OwnerUserID, workspace.DefaultKernelDelegationSpawnCap)
	if err != nil || stats.SpawnedThisTask != 1 || providerCalls.Load() != 0 {
		t.Errorf("replay spawned extra work: stats=%#v provider_calls=%d err=%v", stats, providerCalls.Load(), err)
	}
}

func startDelegateInitializationBatch(t *testing.T, f *agentSaveArtifactsFixture, outerID string) {
	t.Helper()
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{
		map[string]any{"id": outerID, "type": "function", "name": "python", "arguments": map[string]any{"code": "pass"}},
	}})
	var batch workspace.ToolCallBatch
	var items []workspace.ToolCallBatchItem
	_, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: outerID + ":batch", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = f.store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}, err
		},
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("real tool batch=%#v items=%#v err=%v", batch, items, err)
	}
	batch, err = f.store.ClaimToolCallBatch(ctx, workspace.ClaimToolCallBatchInput{Claim: f.claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	started, _ := json.Marshal(map[string]any{"status": "running", "toolCallId": outerID, "toolName": "python", "toolPhase": "start"})
	_, _, _, err = f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: outerID + ":start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: started,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := f.store.StartToolCallBatchItemTx(ctx, tx, workspace.StartToolCallBatchItemInput{
				Claim: f.claim, BatchID: batch.BatchID, Ordinal: items[0].Ordinal,
				ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: items[0].StateVersion, StartedEvent: event,
			})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKernelDelegateInitializationFailureSettlesOnlyOriginalPreclaim(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	const outerID, model = "initial-failure-cell", "initial-failure-model"
	startDelegateInitializationBatch(t, f, outerID)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	configureKernelHostProvider(t, f.server, f.identity.access, "initial-failure-provider", "openai", provider.URL+"/v1", model, "initial-failure-secret", "synthetic-key")
	index := filepath.Join(f.server.fileRoot, "sessions", "index.json")
	if err := os.MkdirAll(filepath.Dir(index), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("{invalid session index"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound := kernelHostExecutionIdentity{access: f.identity.access, transcriptExecution: kernelTranscriptExecutionBinding{
		stream: f.stream, claim: f.claim, outerToolUseID: outerID, kernelID: "initial-failure-kernel", kernelGeneration: 1,
		currentKernelGeneration: func() uint64 { return 1 },
	}}
	result, err := f.server.handleKernelSupervisionHostCall(ctx, bound, f.identity.access, "host.delegate",
		[]any{[]any{map[string]any{"task": "Initial input never dispatched", "model": model}}, map[string]any{"wait": false}}, nil, "initial-failure-call", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := result.([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("delegate result=%#v", result)
	}
	response := rows[0].(map[string]any)
	failure, _ := response["error"].(string)
	if response["status"] != "failed" || response["dispatched"] != false || failure == "" {
		t.Errorf("initialization failure claimed successful dispatch: %#v", response)
	}
	frameID, _ := rows[0].(map[string]any)["frame_id"].(string)
	child, found, err := f.store.GetKernelSupervisedChild(ctx, f.stream.FrameID, frameID, f.stream.OwnerID)
	if err != nil || !found || child.Status != "failed" || child.Error == "" || child.CompletedAt == nil {
		t.Fatalf("original initialization error was lost: child=%#v err=%v", child, err)
	}
	initial, err := f.store.KernelChildInitialTarget(ctx, child)
	if err != nil || initial == nil || initial.InputRevision != 1 || initial.RunnerAttempt != 0 {
		t.Fatalf("original input proof changed: target=%#v err=%v", initial, err)
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(ctx, f.claim); err != nil || providerCalls.Load() != 0 {
		t.Fatalf("initialization failure affected parent or executed provider: parent=%v calls=%d", err, providerCalls.Load())
	}
}
