package workspace

import (
	"context"
	"fmt"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerTaskMetricsAuthoritySeparatesTaskTotalsFromLatestInputRevision(t *testing.T) {
	store, repo, claim := newToolCallBatchFixture(t)

	metrics, found, err := repo.GetRunnerTaskMetricsAuthority(
		context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision,
	)
	if err != nil || !found || len(metrics.TaskAttempts) != 1 || len(metrics.LatestAttempts) != 1 ||
		metrics.TaskAttempts[0] != claim.Attempt || metrics.LatestAttempts[0] != claim.Attempt ||
		metrics.TaskToolCallCount != 0 || metrics.LatestToolCallCount != 0 {
		t.Fatalf("metrics=%#v found=%t err=%v", metrics, found, err)
	}

	createToolCallBatchForTest(
		t, store, repo, claim, "task-metrics-batch",
		toolCallBatchTestCall{id: "task-metrics-read", name: "read_file", arguments: map[string]any{"path": "notes.txt"}},
		toolCallBatchTestCall{id: "task-metrics-python", name: "python", arguments: map[string]any{"code": "print(1)"}},
		toolCallBatchTestCall{id: "task-metrics-save", name: "save_artifact", arguments: map[string]any{"name": "result.txt"}},
	)

	if _, _, created, err := repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claim, ClientMessageID: "task-metrics-first-finished", Status: "completed",
		PayloadJSON: []byte(`{"summary":"first round complete"}`),
	}); err != nil || !created {
		t.Fatalf("finish first created=%t err=%v", created, err)
	}
	if _, created, err := repo.AppendUserEvent(context.Background(), transcriptstore.AppendUserEventInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: "task-metrics-second-input",
		PayloadJSON: []byte(`{"text":"continue"}`),
	}); err != nil || !created {
		t.Fatalf("append second input created=%t err=%v", created, err)
	}
	metrics, found, err = repo.GetRunnerTaskMetricsAuthority(
		context.Background(), claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision+1,
	)
	if err != nil || !found || len(metrics.LatestAttempts) != 0 ||
		metrics.TaskToolCallCount != 3 || metrics.LatestToolCallCount != 0 {
		t.Fatalf("pending next input metrics=%#v found=%t err=%v", metrics, found, err)
	}
	second, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "task-metrics-runner-2", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !second.Claimed || second.Claim.ClaimedInputRevision <= claim.ClaimedInputRevision {
		t.Fatalf("second claim=%#v err=%v", second, err)
	}
	createToolCallBatchForTest(
		t, store, repo, second.Claim, "task-metrics-second-batch",
		toolCallBatchTestCall{id: "task-metrics-second-read", name: "read_file", arguments: map[string]any{"path": "result.txt"}},
		toolCallBatchTestCall{id: "task-metrics-second-save", name: "save_artifact", arguments: map[string]any{"name": "final.txt"}},
	)

	metrics, found, err = repo.GetRunnerTaskMetricsAuthority(
		context.Background(), claim.StreamUID, claim.OwnerID, second.Claim.ClaimedInputRevision,
	)
	if err != nil || !found || len(metrics.TaskAttempts) != 2 || len(metrics.LatestAttempts) != 1 ||
		metrics.TaskAttempts[0] != claim.Attempt || metrics.TaskAttempts[1] != second.Claim.Attempt ||
		metrics.LatestAttempts[0] != second.Claim.Attempt || metrics.TaskToolCallCount != 5 ||
		metrics.LatestToolCallCount != 2 {
		t.Fatalf("metrics=%#v found=%t err=%v", metrics, found, err)
	}
}

func TestRunnerTaskMetricsAuthorityKeepsCallsAcrossApprovalRevisions(t *testing.T) {
	store, repo, claim := newToolCallBatchFixture(t)
	ctx := context.Background()
	originalAttempt := claim.Attempt
	for round := 1; round <= 2; round++ {
		clientID := fmt.Sprintf("metrics-approval-batch-%d", round)
		calls := []toolCallBatchTestCall{
			{id: clientID + "-read", name: "read_file", arguments: map[string]any{"path": "notes.txt"}},
			{id: clientID + "-save", name: "save_artifact", arguments: map[string]any{"name": "result.txt"}},
		}
		batch, _ := createToolCallBatchForTest(t, store, repo, claim, clientID, calls...)
		replayed, _ := createToolCallBatchForTest(t, store, repo, claim, clientID, calls...)
		if replayed.BatchID != batch.BatchID {
			t.Fatalf("replayed batch=%q original=%q", replayed.BatchID, batch.BatchID)
		}
		pause, _, created, err := repo.PauseRunnerForInput(ctx, transcriptstore.AppendRunnerCheckpointInput{
			Claim: claim, ClientMessageID: fmt.Sprintf("metrics-approval-wait-%d", round),
			Phase: transcriptstore.RunnerPhaseWaitingUser, Resumable: true,
			PayloadJSON: []byte(`{"status":"awaiting_user_response"}`),
		})
		if err != nil || !created {
			t.Fatalf("pause=%#v created=%t err=%v", pause, created, err)
		}
		if _, err := store.db.Exec(`UPDATE frames SET status='awaiting_user_response' WHERE id='tool-batch-frame'`); err != nil {
			t.Fatal(err)
		}
		answerID := fmt.Sprintf("metrics-approval-answer-%d", round)
		answer, _, created, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
			StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: answerID,
			FrameEventID: answerID + "-event", MessageUUID: answerID + "-message", Text: "Approve this operation.",
		})
		if err != nil || !created || answer.Type != "user_input_response" {
			t.Fatalf("answer type=%q created=%t err=%v", answer.Type, created, err)
		}
		resumed, err := repo.ClaimNextFrameRunner(ctx, transcriptstore.ClaimNextFrameRunnerInput{
			RunnerID: fmt.Sprintf("metrics-approved-runner-%d", round), TTL: time.Minute,
		})
		if err != nil || !resumed.Claimed || resumed.Claim.Attempt != originalAttempt ||
			resumed.Claim.ClaimedInputRevision != claim.ClaimedInputRevision+1 ||
			resumed.Claim.ResumeCheckpoint != pause.Sequence {
			t.Fatalf("resumed=%#v err=%v", resumed, err)
		}
		claim = resumed.Claim
		metrics, found, err := repo.GetRunnerTaskMetricsAuthority(ctx, claim.StreamUID, claim.OwnerID, claim.ClaimedInputRevision)
		if err != nil || !found || len(metrics.LatestAttempts) != 1 || metrics.LatestAttempts[0] != originalAttempt ||
			metrics.TaskToolCallCount != int64(round*2) || metrics.LatestToolCallCount != int64(round*2) {
			t.Fatalf("approval round=%d metrics=%#v found=%t err=%v", round, metrics, found, err)
		}
	}
}
