package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func interruptTextToolEnvelopeForTest(t *testing.T, f *correctionRouteFixture, id string) *SessionRunnerCycleResult {
	t.Helper()
	claim := f.run.Transcript.Claim
	appendRunnerToolCheckpoint(t, f.repo, claim, id, map[string]any{
		"status": "running", "stage": "model_execution", "lifecyclePhase": "provider",
	})
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), f.run.Transcript, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	result := &SessionRunnerCycleResult{SessionID: f.run.SessionID, Attempt: f.run.Attempt}
	var callErr error = &agentruntime.TextToolEnvelopePresentationError{}
	handled, err := f.server.handleSessionRunnerChatInterruption(context.Background(),
		SessionRunnerChatOptions{SessionID: f.run.SessionID, RunnerID: claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, f.run.Transcript, f.run, entries, &callErr, nil)
	if err != nil || !handled || result.Status != "interrupted" {
		t.Fatalf("text-envelope interruption: result=%#v handled=%t err=%v", result, handled, err)
	}
	return result
}

func parkRepeatedTextToolEnvelopesForTest(t *testing.T, f *correctionRouteFixture) *SessionRunnerCycleResult {
	t.Helper()
	for cycle := 0; cycle <= sessionRunnerConsecutiveIdenticalToolRoundBudget; cycle++ {
		result := interruptTextToolEnvelopeForTest(t, f, fmt.Sprintf("text-provider-%d", cycle))
		wantPark := cycle == sessionRunnerConsecutiveIdenticalToolRoundBudget
		if result.AwaitingRecoveryCondition != wantPark || result.InterruptionAutoResume == wantPark {
			projection, err := f.server.loadSessionRunnerRecoveryProjection(context.Background(), f.run.Transcript)
			t.Fatalf("unchanged text-envelope cycle=%d awaiting=%t auto=%t wantPark=%t repetition=%#v projectionErr=%v",
				cycle, result.AwaitingRecoveryCondition, result.InterruptionAutoResume, wantPark, projection.CorrectionRepetition, err)
		}
		if wantPark {
			if _, eligible, err := f.repo.GetAutoResumeCandidate(context.Background(), f.stream.UID, f.stream.OwnerID); err != nil || eligible {
				t.Fatalf("parked text envelope still auto-schedules: eligible=%t err=%v", eligible, err)
			}
			return result
		}
		if cycle == 1 {
			f.reopen(t)
		}
		f.resume(t)
	}
	t.Fatal("text-envelope route never reached its bounded wait")
	return nil
}

func TestTextToolEnvelopeDurableWaitAndChangedConditions(t *testing.T) {
	for _, change := range []string{"unchanged", "material", "input"} {
		t.Run(change, func(t *testing.T) {
			f := newCorrectionRouteFixture(t)
			appendRunnerToolCheckpoint(t, f.repo, f.run.Transcript.Claim, "completed-source", map[string]any{
				"toolName": "read_file", "toolPhase": "completed", "toolCallId": "completed-source",
				"toolResult": map[string]any{"ok": true, "content": "retained synthetic source"},
			})
			result := parkRepeatedTextToolEnvelopesForTest(t, f)
			if result.InterruptionReasonCode != sessionRunnerFinalPresentationReasonCode {
				t.Fatalf("text presentation acquired provider continuation semantics: %q", result.InterruptionReasonCode)
			}
			assertTextToolRecoveryReceiptForTest(t, f)
			checkpoint, found, err := f.repo.LatestResumableCheckpoint(context.Background(), f.stream.UID, f.stream.OwnerID)
			if err != nil || !found {
				t.Fatalf("parked logical task lost checkpoint: found=%t err=%v", found, err)
			}
			if _, err := f.store.CreateAutoResumeDispatch(f.stream.SessionID, f.stream.SessionID, f.stream.ProjectID, "OPERON", "initial_task"); err != nil {
				t.Fatal(err)
			}
			dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("text-recovery-worker", time.Minute)
			if err != nil || !claimed {
				t.Fatalf("recovery dispatch claim=%t err=%v", claimed, err)
			}
			if change == "material" {
				resumed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
					TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
				})
				if err != nil || !resumed.Claimed {
					t.Fatalf("material-evidence owner claim=%#v err=%v", resumed, err)
				}
				f.run = &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(resumed.Claim.Attempt), ClaimToken: resumed.Claim.ClaimToken,
					Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: resumed.Claim}}
				appendRunnerToolCheckpoint(t, f.repo, resumed.Claim, "new-material-evidence", map[string]any{
					"toolName": "python", "toolPhase": "completed", "status": "completed",
					"toolResult": map[string]any{"ok": true, "executed": true, "stdout": "new synthetic result"},
				})
			}
			if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
				ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken,
				ReasonCode: result.InterruptionReasonCode, RunnerAttempt: result.Attempt, CheckpointEventID: checkpoint.EventID,
				WaitingFor: workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition, RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
			}); err != nil {
				t.Fatal(err)
			}
			if change == "input" {
				if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "changed-text-input", FrameEventID: "changed-text-input-event",
					MessageUUID: "changed-text-input-message", Text: "Continue with the corrected input.",
				}); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := f.server.wakeChangedFrameRecoveryWaits(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			current, found, err := f.store.GetCompatibilityFrameResumeDispatch(dispatch.ResumeEvent.ID)
			wantWake := change != "unchanged"
			if err != nil || !found || (current.WaitingFor == "") != wantWake || current.Attempt != dispatch.Attempt {
				t.Fatalf("text recovery wake=%t dispatch=%#v err=%v", wantWake, current, err)
			}
			wakes := 0
			if wantWake {
				wakes = 1
			}
			assertFrameResumeDispatchEvents(t, f.store, f.stream.SessionID, map[string]int{"frame_resume_dispatch_woken": wakes})
		})
	}
}

func assertTextToolRecoveryReceiptForTest(t *testing.T, f *correctionRouteFixture) {
	t.Helper()
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), f.run.Transcript, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if stringValue(entry.Message["toolCallId"]) == "completed-source" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("completed source receipt missing or repeated after recovery: %d", count)
	}
}

func TestTextToolPresentationKeepsExistingArtifactObligation(t *testing.T) {
	f := newCorrectionRouteFixture(t)
	appendRunnerToolCheckpoint(t, f.repo, f.run.Transcript.Claim, "completed-source", map[string]any{
		"toolName": "read_file", "toolPhase": "completed", "toolCallId": "completed-source",
		"toolResult": map[string]any{"ok": true, "content": "retained synthetic source"},
	})
	failure := &sessionRunnerReferenceIntegrityError{CrossArtifactFailures: []string{"unresolved_template_marker:report.md"}}
	obligation := failure.runnerCorrection()
	f.reject(t, failure)
	f.resume(t)
	parkRepeatedTextToolEnvelopesForTest(t, f)
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), f.run.Transcript, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	correction, found := latestRunnerCorrection(entries)
	projection, err := f.server.loadSessionRunnerRecoveryProjection(context.Background(), f.run.Transcript)
	if err != nil || !found || !projection.HasCorrection || correction.ReasonCode != obligation.ReasonCode ||
		runnerCorrectionFingerprint(correction.cause()) != runnerCorrectionFingerprint(obligation) ||
		runnerCorrectionFingerprint(projection.Correction.cause()) != runnerCorrectionFingerprint(obligation) {
		t.Fatalf("presentation replaced outstanding artifact obligation: correction=%#v projection=%#v err=%v", correction, projection.Correction, err)
	}
	if !recoveredRunnerCorrectionRequiresTool(entries) {
		t.Fatal("presentation correction discharged the required artifact mutation")
	}
	assertTextToolRecoveryReceiptForTest(t, f)
}
