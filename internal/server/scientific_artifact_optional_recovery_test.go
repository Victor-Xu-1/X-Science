package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestOptionalScientificRecoveryKeepsTypedInvalidityAndPublishesNoticeOnce(t *testing.T) {
	for _, knownInvalid := range []bool{false, true} {
		name := "optional unavailable"
		if knownInvalid {
			name = "prior confirmed invalid"
		}
		t.Run(name, func(t *testing.T) {
			f, session, _, _, original := optionalScientificCompletionFixture(t)
			ctx := context.Background()
			checkpointPayload := map[string]any{"status": "running"}
			if knownInvalid {
				cause := (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: []string{"invalid_sdf_records"}}).runnerCorrection()
				payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
				if err != nil {
					t.Fatal(err)
				}
				checkpointPayload[transcriptstore.RunnerInterruptionCauseField] = payload
			}
			rawCheckpoint, _ := json.Marshal(checkpointPayload)
			delta, _ := json.Marshal(map[string]any{"text": original, "delta_index": 1, "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
			if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
				Claim: f.claim, ClientMessageID: "chemical-old-delta", Type: "content_delta", Source: transcriptstore.EventSourcePayload, PayloadJSON: delta,
			}); err != nil {
				t.Fatal(err)
			}
			checkpoint, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
				Claim: f.claim, ClientMessageID: "chemical-recovery-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting,
				Resumable: true, PayloadJSON: rawCheckpoint,
			})
			if err != nil {
				t.Fatal(err)
			}
			terminal, _ := json.Marshal(map[string]any{"status": "failed", "detail": "runner completion reference integrity failed",
				"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
			if _, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
				Claim: f.claim, ClientMessageID: "chemical-old-finished", Status: "failed", PayloadJSON: terminal,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
				t.Fatal(err)
			}
			dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("chemical-recovery-worker", time.Minute)
			if err != nil || !found {
				t.Fatalf("Continue dispatch: found=%t err=%v", found, err)
			}
			claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID),
				TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
			})
			if err != nil || !claimed.Claimed {
				t.Fatalf("recovery claim=%#v err=%v", claimed, err)
			}
			stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			run := &sessionRunnerChatRun{SessionID: session.ID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
			for replay := 0; replay < 2; replay++ {
				status, detail, _, handled, err := f.server.recoverTranscriptCompletionOnly(ctx, session, run)
				want := "completed"
				if knownInvalid {
					want = "failed"
				}
				if err != nil || !handled || status != want || (knownInvalid && !strings.Contains(detail, "invalid_sdf_records")) {
					t.Fatalf("old candidate authority lost: status=%s handled=%t detail=%s err=%v", status, handled, detail, err)
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
			answers, deltas := 0, 0
			for _, event := range events {
				var payload map[string]any
				if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
					t.Fatal(err)
				}
				if event.Event.Type == "content_delta" {
					deltas++
					if stringValue(payload["text"]) != original {
						t.Fatal("old candidate was rewritten")
					}
				}
				if event.Event.Type == "assistant_message" {
					answers++
					if text := stringValue(payload["text"]); !strings.HasPrefix(text, original) || strings.Count(text, "Chemical validation was not performed") != 1 {
						t.Fatalf("notice missing or repeated: %q", text)
					}
				}
			}
			wantAnswers := 1
			if knownInvalid {
				wantAnswers = 0
			}
			if answers != wantAnswers || deltas != 1 {
				t.Fatalf("answers=%d want=%d old_deltas=%d", answers, wantAnswers, deltas)
			}
		})
	}
}

func TestFreshReplacementDoesNotInheritUnboundHistoricalScientificFailure(t *testing.T) {
	f, session, run, previous, _ := optionalScientificCompletionFixture(t)
	condition := (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: []string{"invalid_sdf_records"}}).runnerCorrection().Condition
	run.CorrectionCondition = condition
	_, replacement := seedSessionRunnerSDFCommit(t, f, previous.ArtifactID, "library.sdf", validServerEthanolSDF+"\n", transcriptstore.ArtifactRelationProduced)
	if previous.ID == replacement.ID || previous.ContentSHA256 == replacement.ContentSHA256 {
		t.Fatal("fixture must create a distinct immutable replacement")
	}
	content := "Replacement data: [library.sdf]({{artifact:" + replacement.ID + "}})."
	model := &artifactReferenceRepairModel{content: content, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, content) || !strings.Contains(result.FinalMessage.Content, "Chemical validation was not performed") {
		t.Fatalf("unbound old condition became a permanent replacement gate: response=%q err=%v", result.FinalMessage.Content, err)
	}
}

func knownInvalidScientificContinuationFixture(t *testing.T, beforeFinish ...func(*agentSaveArtifactsFixture)) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, workspace.ArtifactVersion, string) {
	t.Helper()
	return knownInvalidScientificContinuationFixtureWithSetup(t, nil, beforeFinish...)
}

func knownInvalidScientificContinuationFixtureWithSetup(t *testing.T, beforeValidation func(*agentSaveArtifactsFixture), beforeFinish ...func(*agentSaveArtifactsFixture)) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, workspace.ArtifactVersion, string) {
	t.Helper()
	f, session, run, previous, _ := optionalScientificCompletionFixture(t)
	ctx := context.Background()
	malformed := strings.Replace(validServerEthanolSDF, "M  END", "BROKEN", 1) + "\n"
	_, version := seedSessionRunnerSDFCommit(t, f, previous.ArtifactID, "library.sdf", malformed, transcriptstore.ArtifactRelationProduced)
	content := "Existing molecular data: [library.sdf]({{artifact:" + version.ID + "}})."
	if beforeValidation != nil {
		beforeValidation(f)
	}
	commits, err := f.server.sessionRunnerArtifactCommitReferences(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	invalid := scientificArtifactValidation{SchemaVersion: 2, Format: "sdf", Code: "invalid_sdf_records",
		RDKitVersion: "test-parser", InvalidRecordCount: 1, DelimiterCount: 1,
		SupplierRecordCount: 1, AtomCountRecords: 1, TerminalDelimiter: true}
	validate := controlledScientificValidator(t, invalid, 2)
	if beforeValidation != nil {
		invalidValidator := validate
		valid := invalid
		valid.OK, valid.Code, valid.ParsedCount, valid.InvalidRecordCount = true, "valid_sdf", 1, 0
		validValidator := controlledScientificValidator(t, valid, 0)
		validate = func(ctx context.Context, reader io.Reader, format string) (scientificArtifactValidation, error) {
			data, err := io.ReadAll(reader)
			if err != nil {
				return scientificArtifactValidation{}, err
			}
			if string(data) == validServerEthanolSDF {
				return validValidator(ctx, strings.NewReader(string(data)), format)
			}
			return invalidValidator(ctx, strings.NewReader(string(data)), format)
		}
	}
	validation, err := inspectScientificArtifactSelection(ctx, f.store, sessionRunnerProjectID(session), commits, content, validate)
	if err != nil || len(validation.Failures) != 1 || validation.Failures[0] != "invalid_sdf_records" {
		t.Fatalf("controlled original validation did not confirm invalidity: result=%#v err=%v", validation, err)
	}
	delta, _ := json.Marshal(map[string]any{"text": content, "delta_index": 1,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{Claim: f.claim,
		ClientMessageID: "chemical-invalid-candidate", Type: "content_delta", Source: transcriptstore.EventSourcePayload,
		PayloadJSON: delta}); err != nil {
		t.Fatal(err)
	}
	cause := (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: validation.Failures}).runnerCorrection()
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"status": "interrupted", transcriptstore.RunnerInterruptionCauseField: payload})
	checkpoint, _, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "chemical-invalid-selection", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal, _ := json.Marshal(map[string]any{"status": "failed", "detail": cause.Detail,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	for _, action := range beforeFinish {
		action(f)
	}
	finished, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: f.claim, ClientMessageID: "chemical-invalid-finished", Status: "failed", PayloadJSON: terminal,
	})
	if err != nil {
		t.Fatal(err)
	}
	var boundEvent int64
	if err := f.db.QueryRow(`SELECT bound_event_id FROM transcript_artifact_commits
		WHERE stream_uid=? AND runner_attempt=? AND version_id=?`, f.stream.UID, f.claim.Attempt, version.ID).Scan(&boundEvent); err != nil || boundEvent != finished.EventID {
		t.Fatalf("failed terminal did not seal the immutable version: bound=%d terminal=%d err=%v", boundEvent, finished.EventID, err)
	}
	if _, err := f.store.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("chemical-invalid-fresh-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("Continue dispatch: found=%t err=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID),
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.Attempt <= f.claim.Attempt || claimed.Claim.ClaimedInputRevision != f.claim.ClaimedInputRevision {
		t.Fatalf("fresh continuation claim=%#v err=%v", claimed, err)
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	run.Transcript = &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}
	run.Attempt, run.ClaimToken = int(claimed.Claim.Attempt), claimed.Claim.ClaimToken
	projection, err := f.server.loadSessionRunnerRecoveryProjection(ctx, run.Transcript)
	if err != nil || !projection.HasCorrection || projection.Correction.Condition == nil {
		t.Fatalf("typed failure was not restored from SQLite: projection=%#v err=%v", projection, err)
	}
	run.restoreCorrection(&projection.Correction)
	_, current, found, err := f.store.GetArtifactVersionMetadata(version.ID)
	if err != nil || !found || current.ID != version.ID || current.ContentSHA256 != version.ContentSHA256 {
		t.Fatalf("fixture changed the confirmed-invalid immutable version: found=%t err=%v", found, err)
	}
	return f, session, run, version, content
}

func TestFreshCompletionKeepsKnownInvalidImmutableScientificSelection(t *testing.T) {
	f, session, run, _, content := knownInvalidScientificContinuationFixture(t)
	model := &artifactReferenceRepairModel{content: content, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	var integrity *sessionRunnerReferenceIntegrityError
	if !errors.As(err, &integrity) || len(integrity.InvalidScientificArtifacts) != 1 || integrity.InvalidScientificArtifacts[0] != "invalid_sdf_records" {
		t.Fatalf("optional parser absence erased known-invalid unchanged selection: final=%q err=%v", result.FinalMessage.Content, err)
	}
	if model.calls != 1 {
		t.Fatalf("known invalidity replayed provider work: calls=%d", model.calls)
	}
}

func TestFreshCompletionDisclosesUnboundHistoricalScientificFailure(t *testing.T) {
	f, session, run, _, content := optionalScientificCompletionFixture(t)
	run.CorrectionCondition = (&sessionRunnerReferenceIntegrityError{InvalidScientificArtifacts: []string{"invalid_sdf_records"}}).runnerCorrection().Condition
	model := &artifactReferenceRepairModel{content: content, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, content) ||
		strings.Count(result.FinalMessage.Content, "Earlier chemical-validation failures could not be matched to the current immutable selection and have not been revalidated.") != 1 {
		t.Fatalf("unbound historical failure was silently waived or permanently imposed: final=%q err=%v", result.FinalMessage.Content, err)
	}
}
