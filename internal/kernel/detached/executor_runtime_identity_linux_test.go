//go:build linux

package detached

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/software/localcontainer"
)

func namespaceRuntimeIdentityFixture(t *testing.T) (*Executor, workspace.KernelDetachedExecutionRequestV1, localcontainer.Environment) {
	t.Helper()
	ctx := context.Background()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{UID: "frame:frame", OwnerID: "owner", ExternalID: "frame", SessionID: "frame", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project", RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "task", FrameEventID: "task-event", MessageUUID: "task-message", Text: "Test durable identity; do not execute a workload."}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim failed: %v", err)
	}
	claim := claimed.Claim
	payload, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{map[string]any{"id": "namespace-runtime", "type": "function", "name": "python", "arguments": map[string]any{"code": "print('not executed')", "environment": "python", "background": true}}}})
	var operations []workspace.KernelLocalOperation
	_, _, _, err = repository.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "checkpoint", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload, CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
		batch, _, _, err := store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
		if err != nil {
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		}
		operations, err = store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
		receipt := transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}
		for _, operation := range operations {
			receipt.KernelOperations = append(receipt.KernelOperations, transcriptstore.RunnerCheckpointKernelOperationReceipt{OperationID: operation.OperationID, Ordinal: operation.ToolCallOrdinal, ToolCallID: operation.ToolCallID, Tool: operation.Tool, InputSHA256: operation.InputSHA256, ApprovalRequestID: operation.ApprovalRequestID, RequestedEventID: "kernel-local-operation-requested:" + operation.OperationID, InitialStateVersion: operation.StateVersion})
		}
		return receipt, err
	}})
	if err != nil || len(operations) != 1 {
		t.Fatalf("operation fixture failed: %v", err)
	}
	operation := operations[0]
	operation, err = store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "namespace-approval", Scope: "once", Source: "user", ActorID: "owner", CurrentClaim: claim})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareKernelLocalOperation(ctx, workspace.PrepareKernelLocalOperationInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: claim, BootID: "namespace-test-boot", KernelID: "namespace-test-kernel", KernelGeneration: 1, ConfinementSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.StartKernelLocalOperation(ctx, workspace.StartKernelLocalOperationInput{
		OwnerUserID: prepared.OwnerUserID, OperationID: prepared.OperationID, ExpectedStateVersion: prepared.StateVersion,
		Claim: claim, BootID: prepared.BootID, ExecutionID: "namespace-test-execution",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := workspace.KernelDetachedExecutionRequestV1{OperationID: started.OperationID, ExecutionID: started.ExecutionID,
		OwnerUserID: started.OwnerUserID, Environment: started.Environment, KernelID: started.KernelID, KernelGeneration: started.KernelGeneration}
	environment := localcontainer.Environment{Name: started.Environment, HostGeneration: "fixture-generation", ImageID: "sha256:" + strings.Repeat("b", 64)}
	return &Executor{Store: store}, request, environment
}

func TestNamespaceRuntimeIdentityRoundTripUsesActualOwnerBoundReceipt(t *testing.T) {
	e, request, environment := namespaceRuntimeIdentityFixture(t)
	identity, previous, err := e.containerRuntimeIdentity(context.Background(), request, environment)
	if err != nil || previous || identity != nil {
		t.Fatalf("fresh identity=%+v previous=%v err=%v", identity, previous, err)
	}
	actual := localcontainer.RuntimeIdentity{ExecutionID: request.ExecutionID, ContainerID: "actual-observed-container",
		Environment: request.Environment, EnvironmentGeneration: environment.HostGeneration, ImageID: environment.ImageID}
	if err := e.recordContainerRuntimeIdentity(request, actual); err != nil {
		t.Fatal(err)
	}
	identity, previous, err = e.containerRuntimeIdentity(context.Background(), request, environment)
	if err != nil || previous || identity == nil || *identity != actual {
		t.Fatalf("recovered=%+v previous=%v err=%v", identity, previous, err)
	}
	operation, found, err := e.Store.GetKernelLocalOperation(context.Background(), request.OwnerUserID, request.OperationID)
	if err != nil || !found || operation.State != workspace.KernelLocalOperationStateStarted || len(operation.ResultJSON) != 0 {
		t.Fatal("identity receipt falsely completed the operation")
	}
}

func TestNamespaceRuntimeIdentityRejectsForeignOwnerAndChangedEnvironment(t *testing.T) {
	e, request, environment := namespaceRuntimeIdentityFixture(t)
	actual := localcontainer.RuntimeIdentity{ExecutionID: request.ExecutionID, ContainerID: "actual-observed-container",
		Environment: request.Environment, EnvironmentGeneration: environment.HostGeneration, ImageID: environment.ImageID}
	if err := e.recordContainerRuntimeIdentity(request, actual); err != nil {
		t.Fatal(err)
	}
	foreign := request
	foreign.OwnerUserID = "foreign-owner"
	if _, _, err := e.containerRuntimeIdentity(context.Background(), foreign, environment); !errors.Is(err, localcontainer.ErrExecutionOutcomeUnconfirmed) {
		t.Fatalf("foreign owner escaped receipt boundary: %v", err)
	}
	environment.HostGeneration = "changed-generation"
	if _, _, err := e.containerRuntimeIdentity(context.Background(), request, environment); !errors.Is(err, localcontainer.ErrExecutionOutcomeUnconfirmed) {
		t.Fatalf("changed generation was accepted: %v", err)
	}
}
