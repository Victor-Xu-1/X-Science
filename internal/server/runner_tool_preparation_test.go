package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestToolPreparationFailureKeepsCauseAndClosedDiagnostic(t *testing.T) {
	for _, stage := range []string{"tool_arguments", "tool_rejection", "tool_checkpoint_authority", "tool_checkpoint_persistence"} {
		cause := &os.PathError{Op: "read", Path: "/private/SECRET", Err: errors.New("internal detail")}
		failure := classifySessionRunnerPreparationFailure(stage, cause)
		var original *os.PathError
		var prepared sessionRunnerPreparationFailure
		if !errors.As(failure, &original) || original != cause || !errors.Is(failure, cause) || !errors.As(failure, &prepared) || prepared.diagnosticStage() != stage {
			t.Fatal("preparation diagnostic lost its original cause or trusted stage")
		}
		reason := sessionRunnerErrorReasonCode(failure)
		if !strings.HasPrefix(reason, "runner_tool_") || strings.Contains(reason, "SECRET") || reason == sessionRunnerExecutionFailedReasonCode || runnerInterruptionMayContinueSameTask(reason) || runnerInterruptionAutoResume(reason) {
			t.Fatalf("diagnostic weakened the failure boundary: %q", reason)
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, transcriptstore.ErrClaimStale, transcriptstore.ErrOwnerMismatch, transcriptstore.ErrEventConflict} {
		failure := classifySessionRunnerPreparationFailure("tool_checkpoint_persistence", cause)
		if !errors.Is(failure, cause) {
			t.Fatal("authority/cancellation identity was lost")
		}
		if (cause == context.Canceled || cause == context.DeadlineExceeded) && failure != cause {
			t.Fatal("cancellation was reclassified")
		}
	}
	unknown := sessionRunnerPreparationFailure{stage: "SECRET / arbitrary", cause: errors.New("private")}
	if unknown.diagnosticStage() != "preparation" {
		t.Fatal("untrusted stage reached diagnostics")
	}
}

func TestToolCheckpointTransactionFailureRemainsFatal(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	ctx := context.Background()
	if _, err := fixture.db.Exec(`CREATE TRIGGER reject_test_batch BEFORE INSERT ON transcript_tool_call_batches BEGIN SELECT RAISE(ABORT,'isolated transaction failure'); END`); err != nil {
		t.Fatal(err)
	}
	call := agentruntime.ToolCall{ID: "rejected-transaction", Name: "bash", Arguments: json.RawMessage(`{}`), RejectedBeforeExecution: true,
		PreExecutionRejection: json.RawMessage(`{"ok":false,"executed":false,"code":"invalid_tool_arguments","message":"Invalid proposal."}`)}
	err := fixture.server.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, fixture.run, []agentruntime.ToolCall{call})
	if err == nil || !errors.Is(err, transcriptstore.ErrSchemaUnavailable) || sessionRunnerErrorReasonCode(err) != "runner_tool_checkpoint_persistence_failed" {
		t.Fatalf("transaction failure was hidden or lost its stage: %v", err)
	}
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_checkpoint'`, fixture.stream.UID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(fixture.run.ToolBatchIDs) != 0 {
		t.Fatal("failed transaction left an executable or replayable checkpoint")
	}
	if _, found, err := fixture.store.GetKernelLocalOperationByToolCall(ctx, fixture.stream.OwnerID, fixture.stream.UID, call.ID); err != nil || found {
		t.Fatalf("transaction failure obtained execution authority: found=%t err=%v", found, err)
	}
}

func TestRejectedCheckpointRequiresRuntimeFeedback(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	call := agentruntime.ToolCall{ID: "invalid-feedback", Name: "bash", Arguments: json.RawMessage(`[]`), RejectedBeforeExecution: true,
		PreExecutionRejection: json.RawMessage(`{"ok":true,"executed":true,"code":"invalid_tool_arguments","message":"Pretend rejection."}`)}
	err := fixture.server.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, fixture.run, []agentruntime.ToolCall{call})
	if err == nil || sessionRunnerErrorReasonCode(err) != "runner_tool_rejection_preparation_failed" || len(fixture.run.ToolBatchIDs) != 0 {
		t.Fatalf("untrusted rejection obtained a durable placeholder: %v", err)
	}
}

func TestToolCheckpointPreservesStaleClaimAndOwnerFences(t *testing.T) {
	for _, ownerMismatch := range []bool{false, true} {
		fixture := newCorrectionRouteFixture(t)
		authority := *fixture.run.Transcript
		want := transcriptstore.ErrClaimStale
		if ownerMismatch {
			authority.Claim.OwnerID = "other-owner"
			want = transcriptstore.ErrOwnerMismatch
		} else {
			authority.Claim.ClaimToken += "-stale"
		}
		run := *fixture.run
		run.Transcript = &authority
		call := agentruntime.ToolCall{ID: "rejected-stale", Name: "bash", Arguments: json.RawMessage(`[]`), RejectedBeforeExecution: true,
			PreExecutionRejection: json.RawMessage(`{"ok":false,"executed":false,"code":"invalid_tool_arguments","message":"Invalid proposal."}`)}
		err := fixture.server.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, &run, []agentruntime.ToolCall{call})
		if !errors.Is(err, want) || len(run.ToolBatchIDs) != 0 {
			t.Fatalf("authority failure changed: expected=%v actual=%v", want, err)
		}
		var checkpoints int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_checkpoint'`, fixture.stream.UID).Scan(&checkpoints); err != nil {
			t.Fatal(err)
		}
		if checkpoints != 0 {
			t.Fatal("stale authority published a rejection")
		}
	}
}

func TestToolPreparationReasonPersistsWithoutPrivateDetail(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	run := fixture.run
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	cause := classifySessionRunnerPreparationFailure("tool_checkpoint_persistence", errors.New("private /path?token=SECRET"))
	err := fixture.server.settleSessionRunnerChatOutcome(context.Background(), SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, run.Transcript, run, sessionstore.Session{ID: run.SessionID}, nil, "", cause, "")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := fixture.repo.GetTerminalProjection(context.Background(), fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
	if err != nil || projection.ReasonCode != "runner_tool_checkpoint_persistence_failed" || strings.Contains(projection.Detail, "SECRET") || strings.Contains(projection.Detail, "/path") || result.Status != "failed" {
		t.Fatalf("safe stage identity was lost at finalization: reason=%q status=%q err=%v", projection.ReasonCode, result.Status, err)
	}
}
