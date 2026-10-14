package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestFrameContinueHTTPPreconditionsAndBusyDoNotMutate(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	parked, _ := parkFrameResumeWakeCaller(t, f)
	app := f.server.Handler()
	path := "/api/frames/" + f.stream.FrameID + "/resume"
	missing := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, map[string]any{}, http.StatusPreconditionRequired)
	if missing["code"] != "RESUME_PRECONDITION_REQUIRED" {
		t.Fatalf("missing=%#v", missing)
	}
	body := compatibilityContinueTestBody(t, app, "/api/frames/"+f.stream.FrameID, f.stream.OwnerID, nil)
	// A real claim removes transient fences but cannot authorize a new Continue
	// command. It must not turn an unaccepted busy request into an accepted noop.
	first := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, body, http.StatusOK)
	claim, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("http-busy", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	busyBody := compatibilityContinueTestBody(t, app, "/api/frames/"+f.stream.FrameID, f.stream.OwnerID, nil)
	before, _ := json.Marshal(claim.ResumeEvent.Payload)
	busy := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, busyBody, http.StatusConflict)
	if busy["code"] != "RESUME_COMMAND_BUSY" {
		t.Fatalf("busy=%#v first=%#v", busy, first)
	}
	current, _, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
	after, _ := json.Marshal(current.ResumeEvent.Payload)
	var count int
	if err != nil || string(before) != string(after) {
		t.Fatalf("busy changed claim: %s -> %s err=%v", before, after, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("accepted=%d err=%v", count, err)
	}
}

func TestFrameContinueHTTPReplayPrecedesDynamicRegistryAndPreservesNewWait(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	parked, checkpoint := parkFrameResumeWakeCaller(t, f)
	if _, err := f.store.CreateAgent(workspace.CreateAgentInput{ID: "continue-custom-agent", UserID: f.stream.OwnerID, Name: "CONTINUE_CUSTOM", DisplayName: "Controlled", Description: "Controlled registry fixture"}); err != nil {
		t.Fatal(err)
	}
	app := f.server.Handler()
	path := "/api/frames/" + f.stream.FrameID + "/resume"
	body := compatibilityContinueTestBody(t, app, "/api/frames/"+f.stream.FrameID, f.stream.OwnerID, map[string]any{"target_agent": "CONTINUE_CUSTOM"})
	first := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, body, http.StatusOK)
	if err := f.store.DeleteAgent(f.stream.OwnerID, "CONTINUE_CUSTOM"); err != nil {
		t.Fatal(err)
	}
	claim, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("http-next-wait", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken, ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode,
		RunnerAttempt: int(checkpoint.Attempt), CheckpointEventID: checkpoint.EventID, WaitingFor: workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition,
	}); err != nil {
		t.Fatal(err)
	}
	later, _, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(later.ResumeEvent.Payload)
	replay := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, body, http.StatusOK)
	firstReceipt := first["continue_command"].(map[string]any)
	receipt := replay["continue_command"].(map[string]any)
	if receipt["id"] != firstReceipt["id"] || receipt["replayed"] != true {
		t.Fatalf("rebound receipt=%#v first=%#v", receipt, firstReceipt)
	}
	current, _, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
	after, _ := json.Marshal(current.ResumeEvent.Payload)
	if err != nil || string(before) != string(after) {
		t.Fatalf("retry changed W2: %s -> %s err=%v", before, after, err)
	}
	conflictBody := make(map[string]any, len(body))
	for key, value := range body {
		conflictBody[key] = value
	}
	conflictBody["model"] = "changed-control"
	conflict := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, conflictBody, http.StatusConflict)
	if conflict["code"] != "RESUME_COMMAND_CONFLICT" {
		t.Fatalf("conflict=%#v", conflict)
	}
	// New command key against an old observed target is stale, not silently
	// rebound to W2; omit the now-deleted dynamic agent to reach target validation.
	staleBody := map[string]any{"client_command_id": "d8f6f573-846c-49eb-963d-c838783985f6", "resume_target": body["resume_target"]}
	stale := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, staleBody, http.StatusConflict)
	if stale["code"] != "RESUME_TARGET_STALE" {
		t.Fatalf("stale=%#v", stale)
	}
}

func TestFrameContinueHTTPPostCommitPublicationRetryCannotWakeNextWait(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	parked, checkpoint := parkFrameResumeWakeCaller(t, f)
	app := f.server.Handler()
	path := "/api/frames/" + f.stream.FrameID + "/resume"
	body := compatibilityContinueTestBody(t, app, "/api/frames/"+f.stream.FrameID, f.stream.OwnerID, nil)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_continue_publication BEFORE INSERT ON realtime_events BEGIN SELECT RAISE(ABORT,'controlled publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, body, http.StatusInternalServerError)
	var commands int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&commands); err != nil || commands != 1 {
		t.Fatalf("accepted=%d err=%v", commands, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_continue_publication`); err != nil {
		t.Fatal(err)
	}
	claim, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("http-after-publication-failure", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken, ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode,
		RunnerAttempt: int(checkpoint.Attempt), CheckpointEventID: checkpoint.EventID, WaitingFor: workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition,
	}); err != nil {
		t.Fatal(err)
	}
	later, _, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(later.ResumeEvent.Payload)
	replay := compatJSONRequest(t, app, http.MethodPost, path, f.stream.OwnerID, body, http.StatusOK)
	if replay["continue_command"].(map[string]any)["replayed"] != true {
		t.Fatalf("retry=%#v", replay)
	}
	current, _, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
	after, _ := json.Marshal(current.ResumeEvent.Payload)
	if err != nil || string(before) != string(after) {
		t.Fatalf("publication retry changed W2: %s -> %s err=%v", before, after, err)
	}
}
