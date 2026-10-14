package workspace

import (
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelRecoveryNewInputUsesDurableApprovalOrder(t *testing.T) {
	for _, laterApproval := range []bool{false, true} {
		name := "clock_skew_does_not_preserve_old_work"
		if laterApproval {
			name = "later_explicit_approval_survives_another_response"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, repo, prior := newKernelLocalOperationFixture(t)
			// Deliberately disagree with the transcript clock. Wall-clock order is
			// not the admission or supersession authority.
			store.now = func() time.Time { return time.Now().UTC().Add(time.Hour) }
			operation := createKernelLocalOperationForTest(t, store, repo, prior, "ordered-source", "ordered-call")
			approve := func(admit bool) KernelLocalOperation {
				t.Helper()
				input := ResolveKernelLocalOperationApprovalInput{
					OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "ordered-approval", Scope: "once",
					Source: "policy", ActorID: "system", CurrentClaim: prior,
				}
				if admit {
					input.Source, input.ActorID, input.AdmitRunnerRevision, input.CurrentClaim = "user", "owner", true, transcriptstore.RunnerClaim{}
				}
				approved, err := store.ResolveKernelLocalOperationApproval(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				return approved
			}
			if !laterApproval {
				operation = approve(false)
			}
			claim := prior
			if laterApproval {
				// Infrastructure interruption preserves the pending approval card;
				// user Pause deliberately clears that compatibility projection.
				if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
					Claim: prior, ClientMessageID: "interrupted-before-new-input", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
				}); err != nil {
					t.Fatal(err)
				}
				if _, _, _, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
					StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, ClientMessageID: "new-order-input", FrameEventID: "new-order-frame",
					MessageUUID: "new-order-message", MessageOrigin: "task_intent", Text: "Use the new allocation.",
				}); err != nil {
					t.Fatal(err)
				}
				claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
					StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, RunnerID: "new-order-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
				})
				if err != nil || !claimed.Claimed {
					t.Fatalf("new-order claim=%+v err=%v", claimed, err)
				}
				claim = claimed.Claim
			} else {
				claim = kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
			}
			if laterApproval {
				checkpoint, _, _, err := repo.PauseRunnerForApproval(ctx, transcriptstore.AppendRunnerCheckpointInput{
					Claim: claim, ClientMessageID: "new-input-approval-wait", Phase: transcriptstore.RunnerPhaseWaitingApproval,
					Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_approval"}`),
				})
				if err != nil {
					t.Fatal(err)
				}
				operation = approve(true)
				if err := repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
					_, _, err := tx.AppendFrameInputResponse(ctx, transcriptstore.AppendFrameInputResponseInput{
						StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, FrameID: "frame", ClientMessageID: "ask-user-after-approval",
						PayloadJSON: []byte(`{"role":"user","messageOrigin":"input_response","text":"answer","content":[{"type":"text","text":"answer"}]}`),
					})
					return err
				}); err != nil {
					t.Fatal(err)
				}
				claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
					StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "later-response-runner", TTL: time.Minute,
					ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
				})
				if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision <= operation.AdmittedInputRevision {
					t.Fatalf("later response claim=%+v err=%v", claimed, err)
				}
				claim = claimed.Claim
			}
			runnable, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10)
			if err != nil || len(runnable) != 1 {
				t.Fatalf("runnable=%+v err=%v", runnable, err)
			}
			if laterApproval {
				if runnable[0].State != KernelLocalOperationStateApproved || runnable[0].StateVersion != operation.StateVersion {
					t.Fatal("a newer explicit approval was replaced by an older composer message")
				}
			} else if runnable[0].State != KernelLocalOperationStateCancelled || runnable[0].ReasonCode != "superseded_by_new_user_input" {
				t.Fatal("wall-clock skew prevented supersession despite durable ordering")
			}
		})
	}
}
