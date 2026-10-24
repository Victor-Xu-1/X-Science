package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestCompletionAdvisoryIsLocalizedIdempotentAndCannotReplaceAnAnswer(t *testing.T) {
	advice := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryStructurePreview}
	for _, language := range []string{"en", "zh", "zh-CN", "de", ""} {
		t.Run(language, func(t *testing.T) {
			base := "Result 12.5; [file]({{artifact:00000000-0000-4000-8000-000000000001}})."
			text := appendSessionRunnerCompletionAdvisories(base, language, advice, advice)
			english, chinese := sessionRunnerCompletionAdvisoryText(advice.Code)
			want := english
			if sessionRunnerRequiresChinese(language) {
				want = chinese
			}
			if !strings.HasPrefix(text, base) || strings.Count(text, want) != 1 ||
				appendSessionRunnerCompletionAdvisories(text, language, advice) != text {
				t.Fatalf("advisory changed the answer or duplicated: %q", text)
			}
			if got := appendSessionRunnerCompletionAdvisories(" \n", language, advice); got != " \n" {
				t.Fatalf("notice fabricated a missing final answer: %q", got)
			}
		})
	}
}

func TestCompletionRecoveryPersistsSceneNoticeOnceWithoutRewritingPriorCandidate(t *testing.T) {
	ctx := context.Background()
	srv, session, priorAuthority := completionIntentRecoveryFixture(t, "Explain the result in chat", false)
	writeSceneTestArtifact(t, srv.workspaceStore, "base", session.Project.ID, "base.pdb", "ATOM base", session.ID)
	writeSceneTestArtifact(t, srv.workspaceStore, "derived", session.Project.ID, "derived.pdb", "ATOM derived", session.ID)
	prior, found, err := srv.loadTranscriptCompletionRecoveryCandidate(ctx, priorAuthority)
	if err != nil || !found {
		t.Fatalf("prior candidate: found=%t error=%v", found, err)
	}
	// Obtain real Continue and dispatch authority; never use the read-only
	// fixture's synthetic next-attempt claim to write a response.
	if _, err := srv.workspaceStore.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := srv.workspaceStore.ClaimNextCompatibilityFrameResumeDispatch("scene-notice-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("resume dispatch: found=%t error=%v", found, err)
	}
	claimed, err := srv.transcriptStore.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: priorAuthority.Stream.UID, OwnerID: priorAuthority.Stream.OwnerID,
		RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID), TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: priorAuthority.Claim.ResumeCheckpoint,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("real recovery claim=%#v error=%v", claimed, err)
	}
	stream, err := srv.transcriptStore.GetStream(ctx, priorAuthority.Stream.UID, priorAuthority.Stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{SessionID: session.ID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
	for replay := 0; replay < 2; replay++ {
		status, _, _, handled, err := consumeCompletionRecoveryFixture(ctx, srv, session, run)
		if err != nil || !handled || status != "completed" {
			t.Fatalf("replay=%d status=%s handled=%t error=%v", replay, status, handled, err)
		}
	}
	stream, err = srv.transcriptStore.GetStream(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := srv.transcriptStore.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	answers, sourceDeltas := 0, 0
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		switch event.Event.Type {
		case "content_delta":
			sourceDeltas++
			if stringValue(payload["text"]) != prior.Content {
				t.Fatal("old immutable candidate was rewritten")
			}
		case "assistant_message":
			answers++
			text := stringValue(payload["text"])
			if !strings.HasPrefix(text, prior.Content) || strings.Count(text, "Structure preview is unverified") != 1 {
				t.Fatalf("persisted public answer lost its exact candidate or notice: %q", text)
			}
		}
	}
	if answers != 1 || sourceDeltas != 1 {
		t.Fatalf("answers=%d old_deltas=%d", answers, sourceDeltas)
	}
}

func TestToolChoiceViolationDoesNotReviveAdvisorySceneCondition(t *testing.T) {
	f := newCorrectionRouteFixture(t)
	cause := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old auxiliary scene",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{UnboundNames: []string{"scene unavailable"}}})
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "old-scene", map[string]any{
		"status": "interrupted", "reason_code": cause.ReasonCode, "resume_detail": cause.Detail,
		"recovery_contract_revision": sessionRunnerRecoveryContractRevision, transcriptstore.RunnerInterruptionCauseField: payload,
	})
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), f.run.Transcript, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	var chatErr error = &agentruntime.InitialToolChoiceViolationError{}
	result := &SessionRunnerCycleResult{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt)}
	handled, err := f.server.handleSessionRunnerChatInterruption(context.Background(),
		SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, f.run.Transcript, f.run, entries, &chatErr, nil)
	if err != nil || !handled || result.InterruptionReasonCode != sessionRunnerRequiredToolChoiceUnsatisfiedReasonCode {
		t.Fatalf("provider violation revived scene condition: result=%#v handled=%t error=%v", result, handled, err)
	}
}

func TestNewInputDoesNotInheritAnOldSceneCorrection(t *testing.T) {
	cause := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old auxiliary scene",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{UnboundNames: []string{"scene unavailable"}}})
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	entries := []eventjournal.Entry{{Message: eventjournal.Message{
		"type": "runner_checkpoint", "status": "interrupted", "reason_code": cause.ReasonCode,
		"resume_detail": cause.Detail, transcriptstore.RunnerInterruptionCauseField: payload,
	}}, {SourceEventType: "user_message", Message: eventjournal.Message{"type": "message", "role": "user", "text": "Explain a different result"}}}
	if _, found := latestRunnerCorrection(entries); found {
		t.Fatal("new user task inherited the previous scene condition")
	}
	if advisory, err := (&Server{}).recoveredCompletionCorrectionIsAdvisory("", nil, recoveredRunnerCorrection{}); err != nil || advisory {
		t.Fatal("absence of a condition was treated as an advisory obligation")
	}
}
