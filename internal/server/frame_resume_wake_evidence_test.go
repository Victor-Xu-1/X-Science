package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func parkFrameResumeWakeCaller(t *testing.T, f *agentSaveArtifactsFixture) (workspace.CompatibilityFrameResumeDispatch, transcriptstore.RunnerCheckpoint) {
	t.Helper()
	interrupted, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "wake-caller-park", ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode,
		Resumable: true, AutoResume: false, Destinations: []string{transcriptWebDestination},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateAutoResumeDispatch(f.stream.FrameID, f.stream.RootFrameID, f.stream.ProjectID, "OPERON", "test_recovery"); err != nil {
		t.Fatal(err)
	}
	claim, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("wake-caller", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%#v claimed=%t err=%v", claim, claimed, err)
	}
	if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
		ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode, RunnerAttempt: int(f.claim.Attempt),
		CheckpointEventID: interrupted.Event.EventID, WaitingFor: workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition,
		RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	}); err != nil {
		t.Fatal(err)
	}
	parked, found, err := f.store.GetCompatibilityFrameResumeDispatch(claim.ResumeEvent.ID)
	if err != nil || !found {
		t.Fatalf("parked=%#v found=%t err=%v", parked, found, err)
	}
	return parked, interrupted.Checkpoint
}

func TestFrameResumeWakeCallerResolvesExactCandidateWitness(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	_, checkpoint := parkFrameResumeWakeCaller(t, f)
	candidate := transcriptstore.AutoResumeCandidate{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		FrameID: f.stream.FrameID, Attempt: checkpoint.Attempt, CheckpointSequence: checkpoint.Sequence}
	cause := f.server.frameResumeCandidateWakeCause(context.Background(), candidate)
	if checkpoint.EventID == checkpoint.Sequence {
		t.Fatal("fixture does not distinguish event ID and checkpoint sequence")
	}
	if cause.Evidence != "canonical" || cause.EventID != checkpoint.EventID || cause.CheckpointSequence != checkpoint.Sequence {
		t.Fatalf("candidate witness=%#v", cause)
	}
	candidate.CheckpointSequence++
	cause = f.server.frameResumeCandidateWakeCause(context.Background(), candidate)
	if cause.Evidence != "unknown" || cause.ReferenceID != "" || cause.EventID != 0 {
		t.Fatalf("missing coordinate was silently rebound to latest: %#v", cause)
	}
}

func TestFrameResumeWakeCallerReferencesOnlyNewDurableCondition(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	parked, _ := parkFrameResumeWakeCaller(t, f)
	if cause, err := f.server.frameRecoveryWaitStateChange(context.Background(), parked); err != nil || cause != nil {
		t.Fatalf("old messages became a new cause: %#v %v", cause, err)
	}
	fresh, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "wake-new-input", FrameEventID: "wake-new-frame-input",
		MessageUUID: "wake-new-message", Text: "Continue with corrected input.", Destinations: []string{transcriptWebDestination},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.wakeChangedFrameRecoveryWaits(context.Background()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := f.db.QueryRow(`SELECT payload FROM frame_events WHERE frame_id=? AND event_type='frame_resume_dispatch_woken'`, f.stream.FrameID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Source  workspace.FrameResumeWakeSource `json:"source"`
		WaitRef string                          `json:"waitRef"`
		Cause   workspace.FrameResumeWakeCause  `json:"cause"`
	}
	if err := json.Unmarshal([]byte(raw), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Source != workspace.FrameResumeWakeRecoveryCondition || audit.WaitRef != parked.WaitRef || audit.Cause.EventID != fresh.EventID || audit.Cause.StreamUID != f.stream.UID || audit.Cause.Evidence != "canonical" {
		t.Fatalf("condition witness=%#v", audit)
	}
}

func TestFrameResumeWakeCallerContinueHasAcceptedCommandReceipt(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	parked, _ := parkFrameResumeWakeCaller(t, f)
	app := f.server.Handler()
	compatJSONRequest(t, app, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/resume", f.stream.OwnerID, map[string]any{}, http.StatusOK)
	var raw string
	if err := f.db.QueryRow(`SELECT payload FROM frame_events WHERE frame_id=? AND event_type='frame_resume_dispatch_woken'`, f.stream.FrameID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Source  workspace.FrameResumeWakeSource `json:"source"`
		ActorID string                          `json:"actorId"`
		WaitRef string                          `json:"waitRef"`
		Cause   workspace.FrameResumeWakeCause  `json:"cause"`
	}
	if err := json.Unmarshal([]byte(raw), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Source != workspace.FrameResumeWakeContinue || audit.ActorID != f.stream.OwnerID || audit.WaitRef != parked.WaitRef || audit.Cause.Evidence != "canonical" {
		t.Fatalf("Continue audit=%#v", audit)
	}
	var commandFrame, commandActor, commandWait string
	if err := f.db.QueryRow(`SELECT frame_id,json_extract(payload,'$.actorId'),json_extract(payload,'$.waitRef') FROM frame_events WHERE id=? AND event_type='frame_resume_continue_command_accepted'`, audit.Cause.ReferenceID).Scan(&commandFrame, &commandActor, &commandWait); err != nil {
		t.Fatal(err)
	}
	if commandFrame != f.stream.FrameID || commandActor != f.stream.OwnerID || commandWait != parked.WaitRef {
		t.Fatal("accepted command does not bind the actual actor and wait")
	}
	// A no-fence idempotent Continue keeps the existing success path. It must
	// not manufacture a second wake or fail because no wake is needed.
	compatJSONRequest(t, app, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/resume", f.stream.OwnerID, map[string]any{}, http.StatusOK)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type='frame_resume_dispatch_woken'`, f.stream.FrameID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("wake count=%d err=%v", count, err)
	}
}

func TestFrameResumeWakeCallerContractChangeIsObservedBuildEvidence(t *testing.T) {
	cause := frameResumeRecoveryContractWakeCause(sessionRunnerRecoveryContractRevision - 1)
	if cause.Kind != "recovery_contract_revision" || cause.Evidence != "observed" || cause.Revision != sessionRunnerRecoveryContractRevision || cause.RuntimeVersion == "" || cause.ReferenceID != fmt.Sprintf("recovery-contract:%d", sessionRunnerRecoveryContractRevision) {
		t.Fatalf("contract witness=%#v", cause)
	}
}
