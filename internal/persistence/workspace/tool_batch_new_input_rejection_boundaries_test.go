package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestDeferredKernelProposalPreservesExecutionWitnesses(t *testing.T) {
	for _, witness := range []string{"started", "receipt"} {
		t.Run(witness, func(t *testing.T) {
			ctx := context.Background()
			store, repo, prior, batch, items := deferredKernelProposalFixture(t)
			if witness == "started" {
				claimed, err := store.ClaimToolCallBatch(ctx, ClaimToolCallBatchInput{Claim: prior, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
				if err != nil {
					t.Fatal(err)
				}
				appendToolCallBatchStart(t, store, repo, prior, claimed, items[0], "existing-start")
			} else {
				_, _, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
					Claim: prior, ClientMessageID: "existing-effect", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true,
					PayloadJSON: []byte(`{"status":"running","toolCallId":"deferred-first","toolPhase":"completed","toolResult":{"ok":true}}`),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			claim := kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
			rejection, err := store.ReadPendingToolCallRejection(ctx, claim, batch.BatchID, 0)
			if err != nil || rejection.Rejected {
				t.Fatalf("execution witness replaced by a nonexecution result: %+v err=%v", rejection, err)
			}
			// An unrelated existing effect does not grant its unstarted sibling
			// permission to execute the obsolete proposal.
			tail, err := store.ReadPendingToolCallRejection(ctx, claim, batch.BatchID, 1)
			if err != nil || !tail.Rejected {
				t.Fatalf("unstarted sibling escaped new input: %+v err=%v", tail, err)
			}
		})
	}
}

func TestDeferredKernelProposalAskUserResponseDoesNotSupersede(t *testing.T) {
	ctx := context.Background()
	store, repo, prior, batch, _ := deferredKernelProposalFixture(t)
	checkpoint, _, _, err := repo.PauseRunnerForApproval(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: prior, ClientMessageID: "waiting-for-answer", Phase: transcriptstore.RunnerPhaseWaitingApproval,
		Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_approval"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(ctx, transcriptstore.AppendFrameInputResponseInput{
			StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, FrameID: "frame", ClientMessageID: "answer-only",
			PayloadJSON: []byte(`{"role":"user","messageOrigin":"input_response","text":"answer","content":[{"type":"tool_result","tool_use_id":"ask","content":"answer"}]}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, RunnerID: "answer-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision <= prior.ClaimedInputRevision {
		t.Fatalf("answer claim=%+v err=%v", claimed, err)
	}
	for ordinal := int64(0); ordinal < 2; ordinal++ {
		rejection, err := store.ReadPendingToolCallRejection(ctx, claimed.Claim, batch.BatchID, ordinal)
		if err != nil || rejection.Rejected {
			t.Fatalf("answer misclassified as replacement intent: %+v err=%v", rejection, err)
		}
	}
}

func TestDeferredKernelProposalNewInputRequiresCurrentClaim(t *testing.T) {
	ctx := context.Background()
	store, repo, prior, batch, _ := deferredKernelProposalFixture(t)
	claim := kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
	for _, stale := range []transcriptstore.RunnerClaim{prior, claim} {
		if stale.Attempt == claim.Attempt {
			stale.OwnerID = "foreign-owner"
		}
		if _, err := store.ReadPendingToolCallRejection(ctx, stale, batch.BatchID, 0); err == nil {
			t.Fatal("stale or foreign claim gained new-input supersession authority")
		}
	}
}

func TestDeferredKernelProposalInputArrivingAfterClaimFencesRecovery(t *testing.T) {
	ctx := context.Background()
	store, repo, prior, batch, _ := deferredKernelProposalFixture(t)
	claim := kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
	_, _, created, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: "later-composer-input",
		FrameEventID: "later-composer-event", MessageUUID: "later-composer-message", MessageOrigin: "task_intent",
		Text: "A newer instruction arrived after the recovery claim.",
	})
	if err != nil || !created {
		t.Fatalf("later input created=%t err=%v", created, err)
	}
	stream, err := repo.ValidateLiveRunnerClaim(ctx, claim)
	if err != nil || stream.InputRevision <= claim.ClaimedInputRevision {
		t.Fatalf("fixture must retain a live claim over an older input: revision=%d claim=%d err=%v", stream.InputRevision, claim.ClaimedInputRevision, err)
	}
	rejection, err := store.ReadPendingToolCallRejection(ctx, claim, batch.BatchID, 0)
	if !errors.Is(err, transcriptstore.ErrClaimStale) || rejection.Rejected {
		t.Fatalf("older live claim consumed an unclaimed input: %+v err=%v", rejection, err)
	}
}
