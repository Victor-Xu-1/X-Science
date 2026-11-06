//go:build linux

package detached

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestHistoricalSpecHandoffKeepsMonotonicAuthorityThroughNextSpecification(t *testing.T) {
	f := newHistoricalHandoffFixture(t, false)
	f.launch.onLaunch = func(request ExecutorLaunchRequest) error {
		current, found, err := f.backend.Store.GetKernelExecutionBackend(t.Context(), request.BackendID)
		if err != nil || !found {
			return err
		}
		ready, err := f.backend.Store.ActivateKernelExecutionBackend(t.Context(), workspace.ActivateKernelExecutionBackendInput{
			BackendID: current.BackendID, BackendGeneration: current.BackendGeneration, ExecutorInstanceID: current.ExecutorInstanceID,
			ExecutorPID: 1 << 30, ExecutorPIDStartTicks: 1, WorkerPID: 1 << 30, WorkerPIDStartTicks: 1,
			WorkerPGID: 1 << 30, HeartbeatSequence: 1,
		})
		if err == nil {
			f.serveClose(t, ready)
		}
		return err
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	a, err := f.backend.EnsureSession(ctx, f.spec)
	if err != nil || a.KernelGeneration <= f.latest.KernelGeneration {
		t.Fatalf("historical A did not become the latest authority: %+v err=%v", a, err)
	}
	c := f.spec
	c.ResourceMemoryBytes = 1536 << 20
	last, err := f.backend.EnsureSession(ctx, c)
	if err != nil || last.KernelGeneration <= a.KernelGeneration || last.BackendID == a.BackendID {
		t.Fatalf("A -> B -> A -> C lost monotonic replacement: A=%+v C=%+v err=%v", a, last, err)
	}
	retired, _, err := f.backend.Store.GetKernelExecutionBackend(t.Context(), a.BackendID)
	if err != nil || retired.State != workspace.KernelExecutionBackendStateStopped || f.closes.Load() != 2 {
		t.Fatalf("next specification bypassed the revived A: %+v closes=%d err=%v", retired, f.closes.Load(), err)
	}
}

func TestHistoricalSpecHandoffDrainsReadyHistoricalAuthority(t *testing.T) {
	f := newHistoricalHandoffFixture(t, false)
	old, err := f.backend.Store.RecreateKernelExecutionBackend(t.Context(), workspace.RecreateKernelExecutionBackendInput{
		BackendID: f.old.BackendID, ExecutorInstanceID: "historical-ready", MachineBootID: f.latest.MachineBootID,
		SocketPath: f.old.SocketPath, KernelID: f.old.KernelID, KernelGeneration: f.old.KernelGeneration, SessionSpec: durableSessionSpec(f.spec),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, admitted, err := f.backend.Store.AdmitKernelResources(t.Context(), old, f.spec.ResourceMemoryBytes,
		workspace.KernelResourceCapacity{AvailableBytes: 3 << 30, DefaultBytes: 2 << 30, MinimumBytes: executorMemoryFloorBytes}); err != nil || !admitted {
		t.Fatalf("seed historical ready allocation: admitted=%t err=%v", admitted, err)
	}
	old, err = f.backend.Store.ActivateKernelExecutionBackend(t.Context(), workspace.ActivateKernelExecutionBackendInput{
		BackendID: old.BackendID, BackendGeneration: old.BackendGeneration, ExecutorInstanceID: old.ExecutorInstanceID,
		ExecutorPID: 1 << 30, ExecutorPIDStartTicks: 1, WorkerPID: 1 << 30, WorkerPIDStartTicks: 1, WorkerPGID: 1 << 30, HeartbeatSequence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.serveClose(t, old)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := f.backend.EnsureSession(ctx, f.spec); !errors.Is(err, f.launch.err) {
		t.Fatalf("ready historical authority was reused beside a newer generation: %v", err)
	}
	current, _, err := f.backend.Store.GetKernelExecutionBackend(t.Context(), old.BackendID)
	if err != nil || current.State != workspace.KernelExecutionBackendStateStopped || f.closes.Load() != 2 {
		t.Fatalf("historical and newer authorities did not use their fenced close owners: %+v closes=%d err=%v", current, f.closes.Load(), err)
	}
}

func TestHistoricalSpecHandoffPreservesBusyPredecessorOnCancellation(t *testing.T) {
	f := historicalHandoffFixtureWithLiveLatest(t, true, true)
	admit := prepareHistoricalHandoffExecution(t, f)
	if err := admit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.backend.EnsureSession(ctx, f.spec); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stack := make([]byte, 128<<10)
		observed := false
		for _, goroutine := range strings.Split(string(stack[:runtime.Stack(stack, true)]), "\n\n") {
			if strings.Contains(goroutine, "(*Backend).retireKernelSessionPredecessor(") && strings.Contains(goroutine, "[select]") {
				observed = true
				break
			}
		}
		if observed {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("busy predecessor did not retain its handoff authority: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("handoff did not reach the bounded busy-owner wait")
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("handoff lost caller cancellation: %v", err)
	}
	current, _, err := f.backend.Store.GetKernelExecutionBackend(t.Context(), f.latest.BackendID)
	count, countErr := f.backend.Store.CountNonterminalKernelExecutions(t.Context(), f.latest.BackendID, f.latest.BackendGeneration)
	if err != nil || countErr != nil || current.State != workspace.KernelExecutionBackendStateReady || count != 1 || f.closes.Load() != 0 || len(f.launch.launch) != 0 {
		t.Fatalf("busy/accepted predecessor was closed or duplicated: state=%s active=%d closes=%d launches=%d err=%v/%v", current.State, count, f.closes.Load(), len(f.launch.launch), err, countErr)
	}
}

func TestHistoricalSpecHandoffDrainFencesLateDispatch(t *testing.T) {
	f := newHistoricalHandoffFixture(t, false)
	admit := prepareHistoricalHandoffExecution(t, f)
	rejected := make(chan bool, 1)
	f.beforeClose = func(current workspace.KernelExecutionBackend) error {
		err := admit()
		if err == nil {
			return errors.New("an accepted execution entered a draining predecessor")
		}
		count, err := f.backend.Store.CountNonterminalKernelExecutions(t.Context(), current.BackendID, current.BackendGeneration)
		if err != nil || count != 0 {
			return errors.New("late dispatch left an accepted predecessor execution")
		}
		rejected <- true
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := f.backend.EnsureSession(ctx, f.spec); !errors.Is(err, f.launch.err) {
		t.Fatalf("idle handoff failed after fencing late dispatch: %v", err)
	}
	select {
	case <-rejected:
	default:
		t.Fatal("controlled dispatch race did not reach the actual drain fence")
	}
}

// Prepare the real immutable tool/approval/operation chain, then let callers
// choose which side of the original immediate drain transaction accepts it.
// The request is persisted only; no worker or code execution is started.
func prepareHistoricalHandoffExecution(t *testing.T, f *historicalHandoffFixture) func() error {
	t.Helper()
	ctx := t.Context()
	store := f.backend.Store
	repository, err := store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "frame:frame", OwnerID: "owner", ExternalID: "frame", SessionID: "frame", Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: "project", RootFrameID: f.spec.RootFrameID, FrameID: f.spec.FrameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "handoff-input", FrameEventID: "handoff-frame-event",
		MessageUUID: "handoff-message", Text: "Synthetic handoff admission; no workload execution.",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "handoff-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim synthetic handoff: claimed=%t err=%v", claimed.Claimed, err)
	}
	payload, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{map[string]any{
		"id": "handoff-call", "type": "function", "name": "python", "arguments": map[string]any{"code": "pass", "environment": "python"},
	}}})
	var operations []workspace.KernelLocalOperation
	_, _, _, err = repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimed.Claim, ClientMessageID: "handoff-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			batch, _, _, err := store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err = store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			receipt := transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}
			for _, operation := range operations {
				receipt.KernelOperations = append(receipt.KernelOperations, transcriptstore.RunnerCheckpointKernelOperationReceipt{
					OperationID: operation.OperationID, Ordinal: operation.ToolCallOrdinal, ToolCallID: operation.ToolCallID, Tool: operation.Tool,
					InputSHA256: operation.InputSHA256, ApprovalRequestID: operation.ApprovalRequestID,
					RequestedEventID: "kernel-local-operation-requested:" + operation.OperationID, InitialStateVersion: operation.StateVersion,
				})
			}
			return receipt, err
		},
	})
	if err != nil || len(operations) != 1 {
		t.Fatalf("synthetic handoff operation: count=%d err=%v", len(operations), err)
	}
	operation := operations[0]
	operation, err = store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "handoff-approval", Scope: "once", Source: "user", ActorID: "owner", CurrentClaim: claimed.Claim,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareKernelLocalOperation(ctx, workspace.PrepareKernelLocalOperationInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion, Claim: claimed.Claim,
		BootID: f.latest.MachineBootID, KernelID: f.latest.KernelID, KernelGeneration: f.latest.KernelGeneration, ConfinementSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return func() error {
		lease, err := f.backend.AcquireSessionControl(ctx, backendSessionRef(f.latest), time.Minute)
		if err != nil {
			return err
		}
		_, _, err = store.StartDetachedKernelLocalOperation(ctx, workspace.StartDetachedKernelLocalOperationInput{
			Start: workspace.StartKernelLocalOperationInput{OwnerUserID: prepared.OwnerUserID, OperationID: prepared.OperationID,
				ExpectedStateVersion: prepared.StateVersion, Claim: claimed.Claim, BootID: prepared.BootID, ExecutionID: "handoff-execution"},
			BackendID: f.latest.BackendID, BackendGeneration: f.latest.BackendGeneration, ControllerEpoch: lease.Epoch, ControllerToken: lease.Token,
			Request: workspace.KernelDetachedExecutionRequestV1{Version: 1, OperationID: prepared.OperationID, ExecutionID: "handoff-execution",
				OwnerUserID: prepared.OwnerUserID, ProjectID: prepared.ProjectID, RootFrameID: prepared.RootFrameID, RootFrameIncarnationID: prepared.RootFrameIncarnationID,
				FrameID: prepared.FrameID, FrameIncarnationID: prepared.FrameIncarnationID, KernelID: f.latest.KernelID, KernelGeneration: f.latest.KernelGeneration,
				ToolCallID: prepared.ToolCallID, ToolName: prepared.Tool, Language: "python", KernelKind: "operon", Environment: "python", Code: "pass", OutputLimitBytes: 1 << 20, Origin: "agent"},
		})
		return err
	}
}
