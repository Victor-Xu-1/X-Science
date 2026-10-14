package workspace

import (
	"context"
	"encoding/json"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelRecoveryNewInputSettlesEntirePendingBatchAtomically(t *testing.T) {
	ctx := context.Background()
	store, repo, prior := newKernelLocalOperationFixture(t)
	var operations []KernelLocalOperation
	payload := []byte(`{"status":"running","modelToolCalls":[{"id":"first","type":"function","name":"python","arguments":{"code":"print(1)","environment":"science"}},{"id":"second","type":"function","name":"bash","arguments":{"command":"true","environment":"science","human_description":"Checking the approved environment"}}]}`)
	_, _, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: prior, ClientMessageID: "pending-batch", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			batch, _, _, err := store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err = store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			return kernelLocalOperationCommitReceiptWithBatch(batch, operations), err
		},
	})
	if err != nil || len(operations) != 2 {
		t.Fatalf("operations=%d err=%v", len(operations), err)
	}
	claim := kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
	// The public preflight API must not inherit the recovery-only allowance for
	// a UI projection removed by Pause.
	if _, err := store.CompleteKernelLocalOperationPreflight(ctx, CompleteKernelLocalOperationPreflightInput{
		OwnerUserID: operations[0].OwnerUserID, OperationID: operations[0].OperationID,
		ExpectedState: operations[0].State, ExpectedStateVersion: operations[0].StateVersion, Claim: claim,
		ReasonCode: "test_preflight", TerminalResultJSON: []byte(`{"ok":true,"executed":false}`),
	}); err == nil {
		t.Fatal("ordinary preflight accepted a missing pending projection")
	}
	projection := kernelLocalOperationApprovalProjection(operations[0])
	projection["input_sha256"] = "conflicting-projection"
	setProjection := func(pending []any) {
		t.Helper()
		if err := repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
			data, err := kernelApprovalContextData(ctx, tx, "frame")
			if err != nil {
				return err
			}
			data["_pending_input_requests"] = pending
			return writeKernelApprovalContextData(ctx, tx, "frame", data)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setProjection([]any{projection})
	if _, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10); err == nil {
		t.Fatal("supersession accepted a present but conflicting pending projection")
	}
	setProjection(nil)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_second_supersession BEFORE INSERT ON kernel_local_operation_materializations WHEN NEW.operation_id='` + operations[1].OperationID + `' BEGIN SELECT RAISE(ABORT,'injected materialization failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10); err == nil {
		t.Fatal("injected write failure was ignored")
	}
	for _, original := range operations {
		current, found, err := store.GetKernelLocalOperation(ctx, original.OwnerUserID, original.OperationID)
		if err != nil || !found || current.State != original.State || current.StateVersion != original.StateVersion {
			t.Fatalf("partial transaction survived: state=%s version=%d err=%v", current.State, current.StateVersion, err)
		}
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_second_supersession`); err != nil {
		t.Fatal(err)
	}
	runnable, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10)
	if err != nil || len(runnable) != 1 || runnable[0].OperationID != operations[0].OperationID {
		t.Fatalf("ordered inventory=%+v err=%v", runnable, err)
	}
	for _, original := range operations {
		current, found, err := store.GetKernelLocalOperation(ctx, original.OwnerUserID, original.OperationID)
		if err != nil || !found || current.State != KernelLocalOperationStateCancelled || current.ApprovalDecision != "deny" || current.ReasonCode != "superseded_by_new_user_input" || current.InputSHA256 != original.InputSHA256 {
			t.Fatalf("pending batch operation not settled: state=%s reason=%s err=%v", current.State, current.ReasonCode, err)
		}
		materialization, found, err := store.GetKernelToolResultMaterialization(ctx, original.OperationID)
		var result struct {
			Executed bool   `json:"executed"`
			Outcome  string `json:"execution_outcome"`
			Revision int64  `json:"recovery_input_revision"`
		}
		if err != nil || !found || json.Unmarshal(materialization.TerminalResultJSON, &result) != nil || result.Executed || result.Outcome != "not_started" || result.Revision != claim.ClaimedInputRevision {
			t.Fatalf("invalid non-execution receipt: %+v err=%v", result, err)
		}
	}
	if _, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10); err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operation_materializations`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("receipt count=%d err=%v", receipts, err)
	}
}
