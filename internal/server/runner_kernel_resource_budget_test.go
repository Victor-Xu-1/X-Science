//go:build linux

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type resourceBudgetHoldingModel struct{ delegate *resourceContinuationModel }

func (m resourceBudgetHoldingModel) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	response, err := m.delegate.Complete(ctx, request)
	if err != nil || len(response.Message.ToolCalls) == 0 {
		return response, err
	}
	var input map[string]any
	if err := json.Unmarshal(response.Message.ToolCalls[0].Arguments, &input); err != nil {
		return response, err
	}
	input["code"] = input["code"].(string) + "\nimport os, time\nwith open('resource-budget-running.txt', 'w') as marker:\n    marker.write('running')\nwhile not os.path.exists('resource-budget-release.txt'):\n    time.sleep(0.02)"
	response.Message.ToolCalls[0].Arguments, err = json.Marshal(input)
	return response, err
}

// Reclaim through the real interruption API without appending a new input or
// generating a new model call. The initial request has reached resource waiting,
// but it has neither prepared an operation nor launched a physical executor.
func reclaimResourceBudgetFixture(t *testing.T) (*resourceContinuationFixture, context.Context, *sessionRunnerChatRun, workspace.KernelLocalOperation) {
	t.Helper()
	x := newResourceContinuationFixture(t)
	x.engine.Model = resourceBudgetHoldingModel{delegate: x.model}
	task, stopTask := context.WithCancel(context.Background())
	t.Cleanup(stopTask)
	request, stopRequest := context.WithCancel(task)
	t.Cleanup(stopRequest)
	done := x.start(context.WithValue(request, sessionRunLifetimeContextKey{}, task))
	select {
	case <-x.launcher.observed:
	case err := <-done:
		t.Fatalf("initial invocation ended before resource waiting: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("initial invocation did not reach resource waiting")
	}
	callID := <-x.callIDs
	stopRequest()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("old invocation cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old invocation did not release its waiter")
	}
	before, found, err := x.f.store.GetKernelLocalOperationByToolCall(task, x.f.stream.OwnerID, x.f.stream.UID, callID)
	if err != nil || !found || before.State != workspace.KernelLocalOperationStateApproved ||
		before.PreparedAt != nil || before.ExecutionID != "" || x.launcher.launched.Load() || task.Err() != nil {
		t.Fatalf("initial source is not unstarted approval: state=%q execution=%q found=%t err=%v", before.State, before.ExecutionID, found, err)
	}
	interrupted, err := x.f.repo.InterruptRunner(task, transcriptstore.InterruptRunnerInput{
		Claim: x.f.claim, ClientMessageID: "resource-budget-interrupted",
		ReasonCode: "runtime_draining", Resumable: true, AutoResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	reclaimed, err := x.f.repo.ClaimRunner(task, transcriptstore.ClaimRunnerInput{
		StreamUID: x.f.stream.UID, OwnerID: x.f.stream.OwnerID, RunnerID: "resource-budget-recovery",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint,
		ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !reclaimed.Claimed || reclaimed.Claim.Attempt != x.f.claim.Attempt ||
		reclaimed.Claim.ClaimedInputRevision != x.f.claim.ClaimedInputRevision ||
		reclaimed.Claim.ClaimToken == x.f.claim.ClaimToken {
		t.Fatalf("same-input reclaim changed lineage: claim=%+v err=%v", reclaimed, err)
	}
	run := &sessionRunnerChatRun{
		SessionID: x.f.stream.SessionID, Attempt: int(reclaimed.Claim.Attempt), ClaimToken: reclaimed.Claim.ClaimToken,
		Transcript:                         &transcriptRunnerAuthority{Stream: x.f.stream, Claim: reclaimed.Claim},
		managedEnvironmentBindingsHydrated: true,
	}
	return x, context.WithValue(task, sessionRunLifetimeContextKey{}, task), run, before
}

func TestRecoveredKernelResourceWaitDoesNotSpendPreparationBudget(t *testing.T) {
	x, task, run, before := reclaimResourceBudgetFixture(t)
	const preparationBudget = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(task)
	defer cancel()
	options := SessionRunnerChatOptions{
		RunnerID: run.Transcript.Claim.RunnerID, SessionID: x.f.stream.SessionID,
		OutputLimitBytes: defaultSessionRunnerOutputLimitBytes,
	}
	type recoveryResult struct {
		resumed bool
		err     error
	}
	done := make(chan recoveryResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		resumed, err := x.f.server.resumeApprovedAgentKernelOperationsWithPreparationBudget(ctx, options, run, preparationBudget)
		done <- recoveryResult{resumed: resumed, err: err}
	}()
	defer func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("recovery waiter did not stop during cleanup")
		}
	}()
	// Recovery owns its reporter. Observe the real persisted checkpoint for the
	// new claim rather than injecting an outer reporter that it would replace.
	progressID := transcriptRunnerClientMessageID(run.Transcript.Claim, chatToolCheckpointPhase("progress-000001-running", before.ToolCallID))
	progressDeadline := time.NewTimer(5 * time.Second)
	defer progressDeadline.Stop()
	progressPoll := time.NewTicker(10 * time.Millisecond)
	defer progressPoll.Stop()
	for {
		var count int
		if err := x.f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_events
			WHERE stream_uid=? AND client_message_id=? AND runner_attempt=?
			AND json_extract(payload_json,'$.toolCallId')=? AND json_extract(payload_json,'$.progress.phase')='waiting_for_resources'`,
			x.f.stream.UID, progressID, run.Transcript.Claim.Attempt, before.ToolCallID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("recovery ended before its original resource wait: resumed=%t err=%v", result.resumed, result.err)
		case <-progressDeadline.C:
			t.Fatal("recovery did not persist actual resource waiting under the new claim")
		case <-progressPoll.C:
		}
	}
	select {
	case result := <-done:
		current, _, loadErr := x.f.store.GetKernelLocalOperation(task, before.OwnerUserID, before.OperationID)
		t.Fatalf("host preparation budget ended a live queue: resumed=%t err=%v state=%q reason=%q load=%v", result.resumed, result.err, current.State, current.ReasonCode, loadErr)
	case <-time.After(2 * preparationBudget):
	}
	waitingOperation, found, err := x.f.store.GetKernelLocalOperation(task, before.OwnerUserID, before.OperationID)
	if err != nil || !found || waitingOperation.State != workspace.KernelLocalOperationStateApproved ||
		waitingOperation.InputSHA256 != before.InputSHA256 || waitingOperation.SourceEventID != before.SourceEventID ||
		waitingOperation.SourceRunnerAttempt != before.SourceRunnerAttempt || waitingOperation.ToolCallID != before.ToolCallID ||
		waitingOperation.AdmittedInputRevision != before.AdmittedInputRevision || waitingOperation.PreparedAt != nil ||
		waitingOperation.StartedAt != nil || waitingOperation.ExecutionID != "" || waitingOperation.ExecutionLogID != "" ||
		waitingOperation.TerminalAt != nil || x.model.requests.Load() != 1 || x.failed.Load() != 0 || x.launcher.launched.Load() {
		t.Fatalf("waiting recovery changed source or fabricated execution: operation=%+v model=%d failed=%d launched=%t found=%t err=%v",
			waitingOperation, x.model.requests.Load(), x.failed.Load(), x.launcher.launched.Load(), found, err)
	}
	x.launcher.available.Store(true)
	if err := x.backend.ReconcileStartups(task); err != nil {
		t.Fatal(err)
	}
	startedDeadline := time.NewTimer(10 * time.Second)
	defer startedDeadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(x.f.projectPath, "resource-budget-running.txt")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case result := <-done:
			t.Fatalf("recovery ended before actual execution ownership: resumed=%t err=%v", result.resumed, result.err)
		case <-startedDeadline.C:
			t.Fatal("admitted code did not reach its physical execution marker")
		case <-progressPoll.C:
		}
	}
	select {
	case result := <-done:
		t.Fatalf("preparation timer cut off an admitted execution: resumed=%t err=%v", result.resumed, result.err)
	case <-time.After(2 * preparationBudget):
	}
	if err := os.WriteFile(filepath.Join(x.f.projectPath, "resource-budget-release.txt"), []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || !result.resumed {
			t.Fatalf("capacity release did not complete the original recovery: resumed=%t err=%v", result.resumed, result.err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("original recovery did not finish after capacity release")
	}
	if _, err := x.f.server.resumeApprovedAgentKernelOperations(task, options, run); err != nil {
		t.Fatal(err)
	}
	after, found, err := x.f.store.GetKernelLocalOperation(task, before.OwnerUserID, before.OperationID)
	contents, readErr := os.ReadFile(filepath.Join(x.f.projectPath, "resource-continuation-count.txt"))
	if err != nil || !found || after.State != workspace.KernelLocalOperationStateCompleted ||
		after.InputSHA256 != before.InputSHA256 || after.SourceEventID != before.SourceEventID ||
		after.SourceRunnerAttempt != before.SourceRunnerAttempt || after.ToolCallID != before.ToolCallID ||
		after.AdmittedInputRevision != before.AdmittedInputRevision || after.ExecutionID == "" ||
		string(contents) != "x" || readErr != nil || x.model.requests.Load() != 1 || x.failed.Load() != 0 {
		t.Fatalf("recovery changed or repeated the original execution: operation=%+v output=%q model=%d failed=%d err=%v read=%v",
			after, contents, x.model.requests.Load(), x.failed.Load(), err, readErr)
	}
}
