package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestQualityRecoveryPersistsOneNoticeWithoutReplayingWork(t *testing.T) {
	f, session, _, _, _ := qualityReportCompletionFixture(t)
	ctx := context.Background()
	checkpoint, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "quality-recovery-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: []byte(`{"status":"running"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	const original = "The recorded quality check failed; existing results are preserved."
	delta, _ := json.Marshal(map[string]any{"text": original, "delta_index": 1,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
		Claim: f.claim, ClientMessageID: "quality-old-delta", Type: "content_delta", Source: transcriptstore.EventSourcePayload, PayloadJSON: delta,
	}); err != nil {
		t.Fatal(err)
	}
	terminal, _ := json.Marshal(map[string]any{"status": "failed",
		"detail":            "runner completion reference integrity failed (cross_artifact_failures=1)",
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "quality-old-finished", Status: "failed", PayloadJSON: terminal,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("quality-recovery-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("actual Continue dispatch: found=%t error=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID),
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("recovery claim=%#v error=%v", claimed, err)
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{SessionID: session.ID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
	for retry := 0; retry < 2; retry++ {
		status, detail, _, handled, err := f.server.recoverTranscriptCompletionOnly(ctx, session, run)
		if err != nil || !handled || status != "completed" {
			t.Fatalf("recovery=%d status=%s handled=%t detail=%s error=%v", retry, status, handled, detail, err)
		}
	}
	stream, err = f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	answers, originalDeltas := 0, 0
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Event.Type == "content_delta" {
			originalDeltas++
			if stringValue(payload["text"]) != original {
				t.Fatal("immutable original response changed")
			}
		}
		if event.Event.Type == "assistant_message" {
			answers++
			text := stringValue(payload["text"])
			if !strings.HasPrefix(text, original) || strings.Count(text, "Result quality is unverified") != 1 {
				t.Fatalf("quality limitation not persisted exactly once: %q", text)
			}
		}
	}
	if answers != 1 || originalDeltas != 1 {
		t.Fatalf("answers=%d original_deltas=%d", answers, originalDeltas)
	}
}

func TestMolecularCompanionCountRecoveryPersistsOriginalOnce(t *testing.T) {
	f, session, _, original := molecularCompanionCountCompletionFixture(t)
	ctx := t.Context()
	delta, _ := json.Marshal(map[string]any{"text": original, "delta_index": 1,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
		Claim: f.claim, ClientMessageID: "count-original-delta", Type: "content_delta", Source: transcriptstore.EventSourcePayload, PayloadJSON: delta,
	}); err != nil {
		t.Fatal(err)
	}
	cause := (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=2 smiles=1"}}).runnerCorrection()
	interruption, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"status": "interrupted", transcriptstore.RunnerInterruptionCauseField: interruption})
	checkpoint, checkpointEvent, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "count-original-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal, _ := json.Marshal(map[string]any{"status": "failed", "detail": cause.Detail,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "count-original-finished", Status: "failed", PayloadJSON: terminal,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("count-recovery-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("actual Continue dispatch: found=%t err=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID),
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("recovery claim=%+v err=%v", claimed, err)
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{SessionID: session.ID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
	for retry := 0; retry < 2; retry++ {
		status, detail, _, handled, err := f.server.recoverTranscriptCompletionOnly(ctx, session, run)
		if err != nil || !handled || status != "completed" {
			t.Fatalf("count recovery=%d status=%s handled=%t detail=%s err=%v", retry, status, handled, detail, err)
		}
	}
	stream, err = f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	answers, originals, historicalConditions := 0, 0, 0
	for _, event := range events {
		var body map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &body); err != nil {
			t.Fatal(err)
		}
		if event.Event.EventID == checkpoint.EventID {
			historicalConditions++
			if !bytes.Equal(checkpointEvent.PayloadJSON, event.Event.PayloadJSON) {
				t.Fatal("recovery rewrote the immutable original checkpoint bytes")
			}
			restored, present, err := transcriptstore.ParseRunnerInterruptionCause(body)
			if err != nil || !present || restored.Condition == nil || restored.Condition.ContentID() != cause.Condition.ContentID() ||
				restored.ReasonCode != cause.ReasonCode || restored.Detail != cause.Detail {
				t.Fatalf("recovery changed the typed count condition identity: present=%t cause=%+v err=%v", present, restored, err)
			}
		}
		if event.Event.Type == "content_delta" {
			originals++
			if stringValue(body["text"]) != original {
				t.Fatal("recovery rewrote the immutable original answer")
			}
		}
		if event.Event.Type == "assistant_message" {
			answers++
			text := stringValue(body["text"])
			if !strings.HasPrefix(text, original) || strings.Count(text, "Result quality is unverified") != 1 || strings.Contains(text, "Chemical validation was not performed") {
				t.Fatalf("recovery lost the original or misreported parsing: %q", text)
			}
		}
	}
	if answers != 1 || originals != 1 || historicalConditions != 1 {
		t.Fatalf("recovery duplicated or replaced original evidence: answers=%d originals=%d conditions=%d", answers, originals, historicalConditions)
	}
	commits, err := f.repo.ListArtifactCommitReferences(ctx, f.stream.UID, f.stream.OwnerID, claimed.Claim.Attempt)
	if err != nil || len(commits) != 0 {
		t.Fatalf("completion-only recovery replayed artifact work: commits=%+v err=%v", commits, err)
	}
}
