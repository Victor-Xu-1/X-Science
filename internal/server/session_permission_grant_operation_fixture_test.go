package server

import (
	"context"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func newSessionPermissionOperationFixture(t *testing.T) (*Server, *workspace.Store, workspace.KernelLocalExecApprovalRequestInput, workspace.KernelLocalOperation) {
	t.Helper()
	app, store, request, claim := newSessionPermissionFixture(t, false)
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var operations []workspace.KernelLocalOperation
	_, _, _, err = repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "session-operation-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true,
		PayloadJSON: []byte(`{"status":"running","modelToolCalls":[{"id":"session-call","type":"function","name":"python","arguments":{"code":"print(1)","environment":"science"}}]}`),
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			batch, _, _, err := store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err = store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			receipt := transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}
			for _, operation := range operations {
				receipt.KernelOperations = append(receipt.KernelOperations, transcriptstore.RunnerCheckpointKernelOperationReceipt{
					OperationID: operation.OperationID, Ordinal: operation.ToolCallOrdinal, ToolCallID: operation.ToolCallID,
					Tool: operation.Tool, InputSHA256: operation.InputSHA256, ApprovalRequestID: operation.ApprovalRequestID,
					RequestedEventID: "kernel-local-operation-requested:" + operation.OperationID, InitialStateVersion: operation.StateVersion,
				})
			}
			return receipt, nil
		},
	})
	if err != nil || len(operations) != 1 {
		t.Fatalf("real operation checkpoint=%#v err=%v", operations, err)
	}
	request.RequestID = operations[0].ApprovalRequestID
	return app, store, request, operations[0]
}
