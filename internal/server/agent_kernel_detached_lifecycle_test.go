package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	kernelruntime "synon-go/internal/kernel"
	kerneldetached "synon-go/internal/kernel/detached"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

type unavailableDetachedBackend struct {
	kernelruntime.ExecutionBackend
	beforeProbe func()
}

func (b unavailableDetachedBackend) Probe(context.Context, kernelruntime.BackendExecutionRef, kernelruntime.BackendControlLease) (kernelruntime.BackendExecutionSnapshot, error) {
	if b.beforeProbe != nil {
		b.beforeProbe()
	}
	return kernelruntime.BackendExecutionSnapshot{}, errors.New("executor socket unavailable")
}

func (unavailableDetachedBackend) AcquireControl(context.Context, kernelruntime.BackendExecutionRef, time.Duration) (kernelruntime.BackendControlLease, kernelruntime.BackendExecutionSnapshot, error) {
	return kernelruntime.BackendControlLease{}, kernelruntime.BackendExecutionSnapshot{}, errors.New("executor socket unavailable")
}

type detachedRecoveryFixture struct {
	server    *Server
	access    workspace.KernelFrameAccess
	operation workspace.KernelLocalOperation
	execution workspace.DetachedKernelExecution
	backend   workspace.KernelExecutionBackend
	claim     transcriptstore.RunnerClaim
	spec      kernelruntime.SessionSpec
	db        *sql.DB
}

func newDetachedRecoveryFixture(t *testing.T, live bool) detachedRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	ticks, err := kernelruntime.CurrentProcessStartTicks()
	if err != nil {
		t.Skipf("process identity unavailable: %v", err)
	}
	if !live {
		ticks++
	} // An old incarnation is dead even when its PID was reused.
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "detached-project", "detached-frame")
	s := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	s.kernelExecutionBackend = unavailableDetachedBackend{}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Close(closeCtx)
	})
	if _, _, err := s.submitFrameMessage(store, frameMessageSubmission{FrameID: "detached-frame", MessageUUID: "detached-message", ClientMessageID: "detached-client", Text: "Run a durable calculation."}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "detached-frame")
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "detached-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	run := &sessionRunnerChatRun{SessionID: stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
	call := agentruntime.ToolCall{ID: "detached-call", Name: "python", Arguments: json.RawMessage(`{"code":"print('result')","environment":"python","background":true}`)}
	if err := s.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: claimed.Claim.RunnerID}, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	op, found, err := store.GetKernelLocalOperationByToolCall(ctx, "local", stream.UID, call.ID)
	if err != nil || !found {
		t.Fatalf("operation found=%t err=%v", found, err)
	}
	op, err = store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{OwnerUserID: "local", OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion, ApprovalRequestID: op.ApprovalRequestID, Approved: true, DecisionID: "detached-decision", Scope: "once", Source: "user", ActorID: "local", CurrentClaim: claimed.Claim})
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.PrepareKernelLocalOperation(ctx, workspace.PrepareKernelLocalOperationInput{OwnerUserID: "local", OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion, Claim: claimed.Claim, BootID: s.kernelOperationBootID, KernelID: "detached-kernel", KernelGeneration: 1, ConfinementSHA256: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	spec := workspace.KernelExecutionSessionSpecV1{Version: 1, KernelID: op.KernelID, OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID, RootFrameID: op.RootFrameID, RootFrameIncarnationID: op.RootFrameIncarnationID, FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID, AgentName: "OPERON", KernelKind: "operon", Language: "python", Environment: "python", WorkspaceDir: t.TempDir(), Mounts: []workspace.KernelExecutionMountSpecV1{}, ProtectedPaths: []string{}}
	backend, err := store.CreateKernelExecutionBackend(ctx, workspace.CreateKernelExecutionBackendInput{BackendID: "detached-backend", OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID, RootFrameID: op.RootFrameID, RootFrameIncarnationID: op.RootFrameIncarnationID, FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID, KernelID: op.KernelID, KernelGeneration: 1, ExecutorInstanceID: "detached-executor", MachineBootID: "test-machine", SocketPath: "/tmp/detached-recovery-test.sock", BackendGeneration: 1, SessionSpec: spec})
	if err != nil {
		t.Fatal(err)
	}
	backend, err = store.ActivateKernelExecutionBackend(ctx, workspace.ActivateKernelExecutionBackendInput{BackendID: backend.BackendID, BackendGeneration: 1, ExecutorInstanceID: backend.ExecutorInstanceID, ExecutorPID: int64(os.Getpid()), ExecutorPIDStartTicks: ticks, WorkerPID: int64(os.Getpid()), WorkerPIDStartTicks: ticks, WorkerPGID: int64(os.Getpid()), HeartbeatSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err := store.AcquireKernelExecutionBackendControl(ctx, workspace.AcquireKernelExecutionBackendControlInput{BackendID: backend.BackendID, BackendGeneration: 1, Token: strings.Repeat("token", 8), LeaseExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	op, execution, err := store.StartDetachedKernelLocalOperation(ctx, workspace.StartDetachedKernelLocalOperationInput{Start: workspace.StartKernelLocalOperationInput{OwnerUserID: "local", OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion, Claim: claimed.Claim, BootID: op.BootID, ExecutionID: "detached-execution"}, BackendID: backend.BackendID, BackendGeneration: 1, ControllerEpoch: lease.Epoch, ControllerToken: lease.Token, Request: workspace.KernelDetachedExecutionRequestV1{Version: 1, OperationID: op.OperationID, ExecutionID: "detached-execution", OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID, RootFrameID: op.RootFrameID, RootFrameIncarnationID: op.RootFrameIncarnationID, FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID, KernelID: op.KernelID, KernelGeneration: 1, ToolCallID: call.ID, ToolName: "python", Language: "python", KernelKind: "operon", Environment: "python", Code: "print('result')", Background: true, Origin: "agent", OutputLimitBytes: 1024, HostCallMethods: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	control := workspace.KernelDetachedExecutionControlInput{ExecutionID: execution.ExecutionID, BackendGeneration: 1, ControllerEpoch: lease.Epoch, ControllerToken: lease.Token, ExpectedVersion: execution.StateVersion}
	execution, err = store.CommitKernelExecutionDispatch(ctx, workspace.CommitKernelExecutionDispatchInput{KernelDetachedExecutionControlInput: control, DispatchSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	control.ExpectedVersion = execution.StateVersion
	execution, err = store.MarkKernelExecutionRequestWritten(ctx, workspace.MarkKernelExecutionRequestWrittenInput{KernelDetachedExecutionControlInput: control, DispatchSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	execution, err = store.MarkKernelExecutionStarted(ctx, workspace.MarkKernelExecutionStartedInput{ExecutionID: execution.ExecutionID, BackendGeneration: 1, ExecutorInstanceID: backend.ExecutorInstanceID, ExpectedVersion: execution.StateVersion, ObservationSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(ctx, op.FrameID)
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	return detachedRecoveryFixture{s, access, op, execution, backend, claimed.Claim, recoveredKernelSessionSpec(spec), db}
}

func (f *detachedRecoveryFixture) commitResult(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	result := kerneldetached.ExecutionResultV1{Version: 1, ExecutionID: f.execution.ExecutionID, ToolCallID: f.operation.ToolCallID, KernelID: f.operation.KernelID, KernelKind: "operon", Language: "python", Environment: "python", StartedAt: now.Add(-time.Second), FinishedAt: now, Response: kernelruntime.Response{Stdout: "result\n"}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	f.execution, _, err = f.server.workspaceStore.CommitKernelExecutionResult(context.Background(), workspace.CommitKernelExecutionResultInput{ReceiptID: "detached-receipt", ExecutionID: f.execution.ExecutionID, BackendGeneration: 1, ExecutorInstanceID: f.backend.ExecutorInstanceID, TerminalSequence: 2, Outcome: workspace.KernelExecutionResultCompleted, ResultJSON: string(raw), ResultSHA256: hex.EncodeToString(digest[:]), StartedAt: result.StartedAt, FinishedAt: now, FilesWrittenJSON: `[]`, DroppedRootsJSON: `[]`})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *detachedRecoveryFixture) wait(ctx context.Context) (map[string]any, error) {
	return f.server.waitDetachedAgentKernelExecution(ctx, f.access, f.spec,
		kernelruntime.BackendSessionRef{BackendID: f.backend.BackendID, BackendGeneration: 1, KernelID: f.operation.KernelID, KernelGeneration: 1, ExecutorInstanceID: f.backend.ExecutorInstanceID},
		kernelruntime.ExecutionStarted{ExecID: f.execution.ExecutionID, ToolUseID: f.operation.ToolCallID, KernelID: f.operation.KernelID, FrameID: f.operation.FrameID, Language: "python", Environment: "python", KernelKind: "operon", Code: "print('result')", Origin: "agent"},
		kernelruntime.BackendExecutionRef{ExecutionID: f.execution.ExecutionID, OperationID: f.operation.OperationID, BackendID: f.backend.BackendID, BackendGeneration: 1, RequestSHA256: f.execution.RequestSHA256, ConfinementSHA256: f.execution.ConfinementSHA256}, kernelruntime.BackendControlLease{}, 1024, &f.operation, &f.claim)
}

func TestDetachedRecoverySettlesDurableResultWithoutExecutorSocket(t *testing.T) {
	f := newDetachedRecoveryFixture(t, false)
	f.commitResult(t)
	originalOperation := f.operation
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := f.wait(ctx)
	if err != nil || result["stdout"] != "result\n" || f.operation.State != workspace.KernelLocalOperationStateCompleted {
		t.Fatalf("durable result stranded: result=%#v state=%s err=%v", result, f.operation.State, err)
	}
	f.operation = originalOperation // Retry the same pre-settlement observation.
	if _, err := f.wait(ctx); err != nil {
		t.Fatalf("terminal replay failed: %v", err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM kernel_local_operation_materializations`,
		`SELECT COUNT(*) FROM notifications WHERE notification_type='cell_result'`,
	} {
		var count int
		if err := f.db.QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("terminal replay count=%d err=%v query=%s", count, err, query)
		}
	}
}

func TestDetachedRecoveryPrefersReceiptCommittedDuringFailedProbe(t *testing.T) {
	f := newDetachedRecoveryFixture(t, false)
	f.server.kernelExecutionBackend = unavailableDetachedBackend{beforeProbe: func() { f.commitResult(t) }}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := f.wait(ctx)
	if err != nil || result["stdout"] != "result\n" || f.operation.State != workspace.KernelLocalOperationStateCompleted {
		t.Fatalf("late receipt lost to executor death: result=%#v operation=%s err=%v", result, f.operation.State, err)
	}
}

func TestDetachedRecoveryDurableReceiptStillRequiresExactReference(t *testing.T) {
	f := newDetachedRecoveryFixture(t, false)
	f.commitResult(t)
	f.execution.RequestSHA256 = strings.Repeat("f", 64)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.wait(ctx); !errors.Is(err, workspace.ErrDetachedKernelExecutionConflict) {
		t.Fatalf("wrong request admitted durable result: %v", err)
	}
	op, _, err := f.server.workspaceStore.GetKernelLocalOperation(ctx, f.operation.OwnerUserID, f.operation.OperationID)
	if err != nil || op.State != workspace.KernelLocalOperationStateStarted {
		t.Fatalf("invalid caller changed operation=%s err=%v", op.State, err)
	}
}

func TestDetachedRecoverySettlesSameBootDeadExecutor(t *testing.T) {
	f := newDetachedRecoveryFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = f.wait(ctx)
	op, _, err := f.server.workspaceStore.GetKernelLocalOperation(ctx, f.operation.OwnerUserID, f.operation.OperationID)
	if err != nil || op.State != workspace.KernelLocalOperationStateOutcomeUnknown {
		t.Fatalf("same-boot dead executor left operation active: state=%s err=%v", op.State, err)
	}
}

func TestDetachedRecoveryRetainsLiveExecutorOnSocketFailure(t *testing.T) {
	f := newDetachedRecoveryFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := f.wait(ctx)
	if !detachedKernelWaitNeedsRecovery(err) {
		t.Fatalf("unreachable live execution lost foreground handoff classification: %v", err)
	}
	op, _, getErr := f.server.workspaceStore.GetKernelLocalOperation(ctx, f.operation.OwnerUserID, f.operation.OperationID)
	if getErr != nil || op.State != workspace.KernelLocalOperationStateStarted {
		t.Fatalf("live execution state=%s err=%v", op.State, getErr)
	}
}

func TestDetachedRecoveryForegroundWaitRetainsOnlyValidAuthority(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled,
		errors.New("socket unavailable"), errors.New("renewal database busy"),
		&detachedKernelTerminalSettlementError{cause: errors.New("settlement database busy")}} {
		if !detachedKernelWaitNeedsRecovery(err) {
			t.Fatalf("accepted execution lost recovery ownership: %v", err)
		}
	}
	for _, err := range []error{nil, workspace.ErrDetachedKernelExecutionConflict,
		workspace.ErrKernelExecutionBackendStale, workspace.ErrKernelExecutionBackendConflict,
		workspace.ErrKernelLocalOperationStale, workspace.ErrKernelLocalOperationConflict,
		transcriptstore.ErrClaimStale,
		&detachedKernelTerminalSettlementError{cause: workspace.ErrDetachedKernelExecutionConflict}} {
		if detachedKernelWaitNeedsRecovery(err) {
			t.Fatalf("rejected authority allowed observer handoff: %v", err)
		}
	}
}
