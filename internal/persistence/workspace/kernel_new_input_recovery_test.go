package workspace

import (
	"bytes"
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func kernelRecoveryComposerInput(t *testing.T, store *Store, repo *transcriptstore.Repository, prior transcriptstore.RunnerClaim, origin string) transcriptstore.RunnerClaim {
	t.Helper()
	ctx := context.Background()
	if _, _, err := store.CancelFrameWithTranscript(ctx, "frame"); err != nil {
		t.Fatal(err)
	}
	event, _, created, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, ClientMessageID: "new-composer-input", FrameEventID: "new-composer-frame-event",
		MessageUUID: "new-composer-message", MessageOrigin: origin, Text: "Use the new explicit allocation before continuing.",
		InputData: map[string]any{"request": "Use the new explicit allocation before continuing."},
	})
	if err != nil || !created {
		t.Fatalf("new composer input created=%t err=%v", created, err)
	}
	runnerID, source := "new-input-runner", transcriptstore.ResumeSourceFresh
	if origin == "input_response" {
		if event.Type != "user_input_response" {
			t.Fatalf("paused composer fixture used the wrong input contract: %s", event.Type)
		}
		if _, err := store.CreateAutoResumeDispatch("frame", "frame", "project", "OPERON", "new_composer_input"); err != nil {
			t.Fatal(err)
		}
		dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("new-input-dispatch", time.Minute)
		if err != nil || !found {
			t.Fatalf("new-input dispatch found=%t err=%v", found, err)
		}
		runnerID, source = "frame-resume:"+dispatch.ResumeEvent.ID, transcriptstore.ResumeSourceUserInput
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, RunnerID: runnerID, TTL: time.Minute, ResumeSource: source,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision <= prior.ClaimedInputRevision {
		t.Fatalf("new-input claim=%+v err=%v", claimed, err)
	}
	return claimed.Claim
}

func TestKernelRecoverySupersedesUnstartedOperationForNewComposerInput(t *testing.T) {
	for _, origin := range []string{"task_intent", "input_response"} {
		t.Run(origin, func(t *testing.T) {
			store, repo, prior := newKernelLocalOperationFixture(t)
			operation := createKernelLocalOperationForTest(t, store, repo, prior, "old-proposal", "old-call")
			approved, err := store.ResolveKernelLocalOperationApproval(context.Background(), ResolveKernelLocalOperationApprovalInput{
				OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
				ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "old-approval", Scope: "once",
				Source: "policy", ActorID: "system", CurrentClaim: prior,
			})
			if err != nil {
				t.Fatal(err)
			}
			claim := kernelRecoveryComposerInput(t, store, repo, prior, origin)
			runnable, err := store.ListRunnableKernelLocalOperations(context.Background(), claim, 10)
			if err != nil || len(runnable) != 1 {
				t.Fatalf("recovery inventory=%+v err=%v", runnable, err)
			}
			settled := runnable[0]
			if settled.OperationID != approved.OperationID || settled.State != KernelLocalOperationStateCancelled || settled.ReasonCode != "superseded_by_new_user_input" {
				t.Fatalf("old unstarted operation still blocks the newer composer input: state=%s reason=%s", settled.State, settled.ReasonCode)
			}
			if !bytes.Equal(settled.InputJSON, approved.InputJSON) || settled.InputSHA256 != approved.InputSHA256 || settled.PreparedAt != nil || settled.StartedAt != nil || settled.ExecutionID != "" {
				t.Fatal("supersession changed immutable input or invented execution")
			}
			materialized, found, err := store.GetKernelToolResultMaterialization(context.Background(), settled.OperationID)
			if err != nil || !found || len(materialized.TerminalResultJSON) == 0 || materialized.ExecutionLogSHA256 != "" {
				t.Fatalf("supersession lost the non-executed protocol result: found=%t err=%v", found, err)
			}
			replayed, err := store.ListRunnableKernelLocalOperations(context.Background(), claim, 10)
			if err != nil || len(replayed) != 1 || replayed[0].StateVersion != settled.StateVersion {
				t.Fatalf("repeated recovery changed settlement: %+v %v", replayed, err)
			}
		})
	}
}
