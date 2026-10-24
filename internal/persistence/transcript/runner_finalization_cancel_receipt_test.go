package transcript

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunnerFinalizationReusesExactCancellationReceipt(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*RunnerFinalizationInput)
		forkBranch bool
		wantErr    error
	}{
		{name: "original_cancel_receipt"},
		{name: "foreign_owner", mutate: func(input *RunnerFinalizationInput) {
			input.Finish.Claim.OwnerID = "foreign-owner"
		}, wantErr: ErrOwnerMismatch},
		{name: "foreign_claim_token", mutate: func(input *RunnerFinalizationInput) {
			input.Finish.Claim.ClaimToken = "foreign-claim-token"
		}, wantErr: ErrClaimStale},
		{name: "wrong_claimed_input", mutate: func(input *RunnerFinalizationInput) {
			input.Finish.Claim.ClaimedInputRevision++
		}, wantErr: ErrClaimStale},
		{name: "cancel_not_on_active_branch", forkBranch: true, wantErr: ErrBranchStateStale},
		{name: "different_destinations", mutate: func(input *RunnerFinalizationInput) {
			input.Finish.Destinations = []string{"ws", "im:other-route"}
		}, wantErr: ErrEventConflict},
		{name: "noncancelled_input", mutate: func(input *RunnerFinalizationInput) {
			input.Finish.Status = "completed"
			input.Finish.PayloadJSON = []byte(`{"status":"completed"}`)
		}, wantErr: ErrClaimStale},
		{name: "assistant_input", mutate: func(input *RunnerFinalizationInput) {
			input.Assistant = &AppendEventInput{
				Claim: input.Finish.Claim, ClientMessageID: "late-assistant", Type: "assistant_message",
				Source: EventSourcePayload, PayloadJSON: []byte(`{"text":"late provider answer"}`), Destinations: []string{"ws"},
			}
		}, wantErr: ErrClaimStale},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, db, stream, source, claim := newFrameBranchForkFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cancelled, err := repo.CancelRunner(ctx, CancelRunnerInput{
				StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ExpectedAttempt: claim.Attempt,
				ClientMessageID: "cancel-1", ReasonCode: "user_cancelled", Destinations: []string{"ws"},
			})
			if err != nil || !cancelled.Applied || cancelled.Event.EventID == 0 {
				t.Fatalf("cancel=%#v err=%v", cancelled, err)
			}
			if test.forkBranch {
				branch, err := repo.GetBranchState(ctx, stream.UID, stream.OwnerID)
				if err != nil {
					t.Fatal(err)
				}
				fork, err := repo.ForkFrameUserMessageBranch(ctx, ForkFrameUserMessageBranchInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID,
					SourceBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
					ClientMutationID: "cancelled-input-edit", SourceClientMessageID: source.ClientMessageID, SourceMessageIndex: 0,
					ReplacementText: "new branch work", Destinations: []string{"ws"},
				})
				if err != nil || !fork.Created || fork.RunnerCancellation.Applied {
					t.Fatalf("fork=%#v err=%v", fork, err)
				}
			}
			input := RunnerFinalizationInput{Finish: FinishRunnerInput{
				Claim: claim, ClientMessageID: "late-runner-finished", Status: "cancelled",
				PayloadJSON: []byte(`{"status":"cancelled","detail":"provider settled after cancellation"}`), Destinations: []string{"ws"},
			}}
			if test.mutate != nil {
				test.mutate(&input)
			}
			eventCounts := func() [3]int {
				t.Helper()
				var counts [3]int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*),
					COALESCE(SUM(event_type='assistant_message'),0),COALESCE(SUM(event_type='runner_finished'),0)
					FROM transcript_events WHERE stream_uid=?`, stream.UID).Scan(&counts[0], &counts[1], &counts[2]); err != nil {
					t.Fatal(err)
				}
				return counts
			}
			before := eventCounts()
			var receipt RunnerFinalizationReceipt
			var created bool
			err = repo.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
				var err error
				receipt, created, err = tx.FinalizeRunner(ctx, input)
				return err
			})
			if !errors.Is(err, test.wantErr) || created {
				t.Fatalf("receipt=%#v created=%t err=%v want=%v", receipt, created, err, test.wantErr)
			}
			if after := eventCounts(); after != before {
				t.Fatalf("finalization added events: before=%v after=%v", before, after)
			}
			if test.wantErr == nil {
				if !sameFinalizationEvent(receipt.Terminal, cancelled.Event) || receipt.Terminal.ClientMessageID != "cancel-1" ||
					receipt.Assistant.EventID != 0 || receipt.Finish.StreamUID != cancelled.Receipt.StreamUID ||
					receipt.Finish.Attempt != cancelled.Receipt.Attempt || receipt.Finish.EventID != cancelled.Receipt.EventID ||
					receipt.Finish.Status != cancelled.Receipt.Status || !receipt.Finish.FinishedAt.Equal(cancelled.Receipt.FinishedAt) {
					t.Fatalf("receipt=%#v original cancellation=%#v", receipt, cancelled)
				}
				// Reusing a pre-existing cancellation is not proof that the late
				// proposed finalization committed after an unknown acknowledgement.
				if _, found, err := repo.ReadRunnerFinalization(ctx, input, &receipt, nil); err != nil || found {
					t.Fatalf("original proposed-result readback found=%t err=%v", found, err)
				}
			}
		})
	}
}
