package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func deferredKernelProposalFixture(t *testing.T) (*Store, *transcriptstore.Repository, transcriptstore.RunnerClaim, ToolCallBatch, []ToolCallBatchItem) {
	t.Helper()
	store, repo, claim := newKernelLocalOperationFixture(t)
	var batch ToolCallBatch
	var items []ToolCallBatchItem
	payload := []byte(`{"status":"running","kernelAdmissionVersion":1,"modelToolCalls":[{"id":"deferred-first","type":"function","name":"python","arguments":{"code":"print(1)","environment":"science"}},{"id":"deferred-second","type":"function","name":"bash","arguments":{"command":"true","environment":"science","human_description":"Check runtime"}}]}`)
	_, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "deferred-proposal", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err := store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			if len(operations) != 0 {
				t.Errorf("modern proposal admitted physical operations prematurely: %d", len(operations))
			}
			return kernelLocalOperationCommitReceiptWithBatch(batch, operations), nil
		},
	})
	if err != nil || len(items) != 2 {
		t.Fatalf("deferred proposal items=%d err=%v", len(items), err)
	}
	return store, repo, claim, batch, items
}

func TestDeferredKernelProposalNewInputPreventsOldTailExecution(t *testing.T) {
	for _, origin := range []string{"task_intent", "input_response"} {
		t.Run(origin, func(t *testing.T) {
			ctx := context.Background()
			store, repo, prior, batch, items := deferredKernelProposalFixture(t)
			unchanged, err := store.ReadPendingToolCallRejection(ctx, prior, batch.BatchID, 0)
			if err != nil || unchanged.Rejected {
				t.Fatalf("same-input proposal rejected: %+v %v", unchanged, err)
			}
			// This fixture is shared with the separately reviewed operation
			// supersession component and uses the real paused-composer APIs.
			claim := kernelRecoveryComposerInput(t, store, repo, prior, origin)
			before, found, err := store.GetToolCallBatch(ctx, prior.OwnerID, batch.BatchID)
			if err != nil || !found {
				t.Fatalf("batch found=%t err=%v", found, err)
			}
			for _, item := range items {
				rejection, err := store.ReadPendingToolCallRejection(ctx, claim, batch.BatchID, item.Ordinal)
				if err != nil || !rejection.Rejected || rejection.ToolCallID != item.ToolCallID || rejection.ToolName != item.ToolName {
					t.Fatalf("old deferred proposal could execute before new input: ordinal=%d rejection=%+v err=%v", item.Ordinal, rejection, err)
				}
				var feedback struct {
					Executed  bool   `json:"executed"`
					Preflight bool   `json:"preflight"`
					Outcome   string `json:"execution_outcome"`
					Status    string `json:"status"`
					Message   string `json:"message"`
				}
				if err := json.Unmarshal(rejection.FeedbackJSON, &feedback); err != nil || feedback.Executed || !feedback.Preflight || feedback.Outcome != "not_started" || feedback.Status != "superseded_by_new_user_input" || feedback.Message == "" {
					t.Fatalf("invalid non-execution feedback: %+v %v", feedback, err)
				}
				replayed, err := store.ReadPendingToolCallRejection(ctx, claim, batch.BatchID, item.Ordinal)
				if err != nil || !bytes.Equal(replayed.FeedbackJSON, rejection.FeedbackJSON) {
					t.Fatalf("unstable supersession feedback: %+v %v", replayed, err)
				}
				if _, exists, err := store.GetKernelLocalOperationByToolCall(ctx, prior.OwnerID, prior.StreamUID, item.ToolCallID); err != nil || exists {
					t.Fatalf("rejection manufactured a kernel operation: exists=%t err=%v", exists, err)
				}
			}
			after, found, err := store.GetToolCallBatch(ctx, prior.OwnerID, batch.BatchID)
			if err != nil || !found || after.StateVersion != before.StateVersion || after.NextOrdinal != before.NextOrdinal {
				t.Fatalf("read-only rejection altered the protocol ledger: %+v %v", after, err)
			}
		})
	}
}
