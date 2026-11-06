package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"syscall"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerArtifactEvidenceRetentionRequiresExactTerminalBinding(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	segment := transcriptstore.AssistantSegmentPayloadV1(1, "")
	assistantPayload := map[string]any{"text": "An unverified synthetic final draft.", "assistant_segment": segment}
	terminal := map[string]any{"status": "failed", "reason_code": sessionRunnerExecutionFailedReasonCode,
		"failure_stage": "artifact_evidence", "retained_final_response_id": "retained-draft", "assistant_segment": segment}
	input := transcriptstore.RunnerFinalizationInput{Assistant: &transcriptstore.AppendEventInput{Claim: fixture.claim,
		ClientMessageID: "retained-draft", Type: "assistant_message", Source: transcriptstore.EventSourcePayload},
		Finish: transcriptstore.FinishRunnerInput{Claim: fixture.claim, ClientMessageID: "retained-failed", Status: "failed"}}
	validate := func() error {
		input.Assistant.PayloadJSON, _ = json.Marshal(assistantPayload)
		input.Finish.PayloadJSON, _ = json.Marshal(terminal)
		return transcriptstore.ValidateRunnerFinalizationInput(input)
	}
	if err := validate(); err != nil {
		t.Fatalf("exact failed evidence binding rejected: %v", err)
	}
	for _, mutation := range []struct {
		key   string
		value any
	}{
		{"retained_final_response_id", "foreign-draft"}, {"failure_stage", "delivery_validation"}, {"failure_stage", nil},
		{"reason_code", "arbitrary_failure"}, {"assistant_segment", transcriptstore.AssistantSegmentPayloadV1(2, "")},
	} {
		old := terminal[mutation.key]
		terminal[mutation.key] = mutation.value
		if err := validate(); !errors.Is(err, transcriptstore.ErrEventConflict) {
			t.Fatalf("invalid %s=%v accepted: %v", mutation.key, mutation.value, err)
		}
		terminal[mutation.key] = old
	}
	terminal["reason_code"] = transcriptstore.RunnerCleanupFailedReason
	if err := validate(); err != nil {
		t.Fatalf("evidence failure followed by cleanup failure rejected: %v", err)
	}
}

func TestRunnerArtifactEvidenceRetentionDoesNotRetainCancelledResponse(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(ErrGenerationStopped)
	cause := &sessionRunnerCompletionFailure{stage: completionArtifactEvidence, err: &os.PathError{Op: "open", Path: "PRIVATE_PATH", Err: syscall.EACCES}}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	if err := fixture.server.settleSessionRunnerChatOutcomeWithCleanup(ctx, SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, "cancelled candidate", cause, "",
		func(context.Context, string, *transcriptRunnerAuthority) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if result.Status != "cancelled" {
		t.Fatalf("cancellation changed to %q", result.Status)
	}
	_, found, err := fixture.repo.GetEventByClientMessageID(t.Context(), fixture.stream.UID, fixture.stream.OwnerID, transcriptRunnerClientMessageID(fixture.claim, "assistant-message"))
	if err != nil || found {
		t.Fatalf("cancelled candidate retained=%t err=%v", found, err)
	}
}
