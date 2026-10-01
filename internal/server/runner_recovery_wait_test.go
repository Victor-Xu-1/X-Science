package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	eventjournal "synon-go/internal/persistence/journal"
	sessionstore "synon-go/internal/persistence/sessions"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestCorrectionProviderOnlyHTTPRecoveryWait(t *testing.T) {
	const frameID = "provider-correction-frame"
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "provider-correction-project", frameID)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "correction-input-message", ClientMessageID: "correction-input",
		Text: "请直接给出已有结果的中文摘要，不要重新计算。",
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, server, "local", frameID)
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid protocol request", http.StatusBadRequest)
			return
		}
		// Invalid native presentation, not a scientific quality judgment. The
		// real provider parser must reject it before publishing a final answer.
		content := agentruntime.PublicProgressEnvelopeBegin + `{"version":2,"id":"invalid","text":"已有结果"}` + agentruntime.PublicProgressEnvelopeEnd
		if strings.Contains(fmt.Sprint(request["messages"]), "Write a brief expert orientation before the task begins.") {
			content = "查看已有证据后给出摘要。"
		} else if requests.Add(1) > 20 {
			http.Error(w, "unbounded correction requests", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}}})
	}))
	defer provider.Close()
	options := SessionRunnerChatOptions{
		SessionID: frameID, RunnerID: "provider-correction-runner", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", LeaseTTL: time.Minute, MaxAttempts: 1,
		DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", frameID)
	if err != nil || !found {
		t.Fatalf("stream found=%t error=%v", found, err)
	}
	for cycle := 0; cycle <= sessionRunnerConsecutiveIdenticalToolRoundBudget; cycle++ {
		result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
		if err != nil || result.Status != "interrupted" {
			t.Fatalf("cycle=%d result=%#v error=%v", cycle, result, err)
		}
		park := cycle == sessionRunnerConsecutiveIdenticalToolRoundBudget
		if result.AwaitingRecoveryCondition != park || result.InterruptionAutoResume == park {
			t.Fatalf("HTTP provider loop cycle=%d result=%#v want park=%t", cycle, result, park)
		}
		options.TranscriptResumeSource = transcriptstore.ResumeSourceCheckpoint
		checkpoint, found, err := repo.LatestResumableCheckpoint(context.Background(), stream.UID, stream.OwnerID)
		if err != nil || !found {
			t.Fatalf("checkpoint found=%t error=%v", found, err)
		}
		options.TranscriptCheckpoint = checkpoint.Sequence
	}
	if requests.Load() != int64(sessionRunnerConsecutiveIdenticalToolRoundBudget+1) {
		t.Fatalf("provider-only repair exceeded its bounded route: requests=%d", requests.Load())
	}
	if err := server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	frame, found, err := store.GetCompatibilityFrame(frameID)
	if err != nil || !found {
		t.Fatalf("frame found=%t error=%v", found, err)
	}
	snapshot, err := server.webConversationRuntimeSnapshot(frame)
	if err != nil || snapshot["state"] != "paused" || snapshot["is_processing"] != false || snapshot["can_send_message"] != true {
		t.Fatalf("parked real-protocol run still appears busy: snapshot=%#v error=%v", snapshot, err)
	}
	if _, eligible, err := repo.GetAutoResumeCandidate(context.Background(), stream.UID, stream.OwnerID); err != nil || eligible {
		t.Fatalf("parked protocol run still schedules: eligible=%t error=%v", eligible, err)
	}
	if _, resumable, err := repo.LatestResumableCheckpoint(context.Background(), stream.UID, stream.OwnerID); err != nil || !resumable {
		t.Fatalf("protocol run lost continuation: resumable=%t error=%v", resumable, err)
	}
}

func TestCorrectionProviderOnlyRecoveryParksUnchangedConditionAfterReopen(t *testing.T) {
	failures := map[string]error{
		"visual":       &sessionRunnerVisualArtifactValidationRequired{Artifacts: []string{"scene.json"}, StructureSceneFailures: []string{"missing version-bound scene"}},
		"presentation": sessionRunnerFinalPresentationCorrection{},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			fixture := newCorrectionRouteFixture(t)
			for cycle := 0; cycle <= sessionRunnerConsecutiveIdenticalToolRoundBudget; cycle++ {
				claim := fixture.run.Transcript.Claim
				appendRunnerToolCheckpoint(t, fixture.repo, claim, fmt.Sprintf("provider-%d", cycle), map[string]any{
					"status": "running", "stage": "model_execution", "lifecyclePhase": "provider",
				})
				entries, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, 20, 20)
				if err != nil {
					t.Fatal(err)
				}
				result := &SessionRunnerCycleResult{SessionID: fixture.run.SessionID, Attempt: fixture.run.Attempt}
				callErr := failure
				handled, err := fixture.server.handleSessionRunnerChatInterruption(context.Background(),
					SessionRunnerChatOptions{SessionID: fixture.run.SessionID, RunnerID: claim.RunnerID}, result,
					&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, fixture.run.Transcript, fixture.run, entries, &callErr, nil)
				if err != nil || !handled || result.Status != "interrupted" {
					t.Fatalf("cycle=%d result=%#v handled=%t error=%v", cycle, result, handled, err)
				}
				park := cycle == sessionRunnerConsecutiveIdenticalToolRoundBudget
				if result.AwaitingRecoveryCondition != park || result.InterruptionAutoResume == park {
					t.Fatalf("unchanged provider-only correction: cycle=%d result=%#v want park=%t", cycle, result, park)
				}
				if park {
					if _, eligible, err := fixture.repo.GetAutoResumeCandidate(context.Background(), fixture.stream.UID, fixture.stream.OwnerID); err != nil || eligible {
						t.Fatalf("parked correction still schedules: eligible=%t error=%v", eligible, err)
					}
					if _, resumable, err := fixture.repo.LatestResumableCheckpoint(context.Background(), fixture.stream.UID, fixture.stream.OwnerID); err != nil || !resumable {
						t.Fatalf("logical task lost continuation: resumable=%t error=%v", resumable, err)
					}
					break
				}
				if cycle == 1 {
					fixture.reopen(t)
				}
				fixture.resume(t)
			}
		})
	}
}

func TestUnchangedRecoveryWithRejectedActionsParksWithoutFailingTask(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	failure := sessionRunnerPlanStepsIncomplete{condition: transcriptstore.RunnerPlanCondition{
		ArtifactID: "plan", VersionID: "plan-version", Steps: []transcriptstore.RunnerPlanConditionStep{{ID: "compute", Title: "Compute result"}},
	}}
	cause := failure.runnerCorrection()
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sessionRunnerConsecutiveIdenticalToolRoundBudget; i++ {
		appendRunnerToolCheckpoint(t, f.repo, f.claim, fmt.Sprintf("rejected-route-%d", i), map[string]any{
			"status": "failed", "toolPhase": "prestart_failed", "toolName": "manage_environments",
			"toolResult": map[string]any{"ok": false, "executed": false, "code": "implementation_selection_required"},
		})
		appendRunnerToolCheckpoint(t, f.repo, f.claim, fmt.Sprintf("unchanged-condition-%d", i), map[string]any{
			"status": "interrupted", "reason_code": cause.ReasonCode, "resume_detail": cause.Detail,
			"recovery_contract_revision": sessionRunnerRecoveryContractRevision, transcriptstore.RunnerInterruptionCauseField: payload,
		})
	}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "latest-rejected-route", map[string]any{
		"status": "failed", "toolPhase": "prestart_failed", "toolName": "manage_environments",
		"toolResult": map[string]any{"ok": false, "executed": false, "code": "implementation_selection_required"},
	})
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority}
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), authority, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	var chatErr error = failure
	result := &SessionRunnerCycleResult{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt)}
	handled, err := f.server.handleSessionRunnerChatInterruption(context.Background(), SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, result, &activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, entries, &chatErr, nil)
	if err != nil || !handled || result.Status != "interrupted" || result.InterruptionAutoResume {
		t.Fatalf("unchanged denied routes kept scheduling model calls: %#v handled=%t error=%v", result, handled, err)
	}
	if _, found, err := f.repo.LatestResumableCheckpoint(context.Background(), f.stream.UID, f.stream.OwnerID); err != nil || !found {
		t.Fatalf("parked recovery lost its resumable checkpoint: %t %v", found, err)
	}
	if _, eligible, err := f.repo.GetAutoResumeCandidate(context.Background(), f.stream.UID, f.stream.OwnerID); err != nil || eligible {
		t.Fatalf("parked recovery was automatically eligible without a state change: %t %v", eligible, err)
	}
}

func TestRecoveryConditionWaitUsesDurableChangesAndPreservesCancellation(t *testing.T) {
	for _, scenario := range []string{"unchanged", "administrative", "material", "user", "upgrade", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			interrupted, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
				Claim: f.claim, ClientMessageID: "park-recovery", ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode,
				Resumable: true, AutoResume: false, Destinations: []string{transcriptWebDestination},
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interrupt: %#v %v", interrupted, err)
			}
			checkpoint, found, err := f.repo.LatestResumableCheckpoint(context.Background(), f.stream.UID, f.stream.OwnerID)
			if err != nil || !found {
				t.Fatalf("checkpoint: %#v %v", checkpoint, err)
			}
			if _, err := f.store.CreateAutoResumeDispatch(f.stream.SessionID, f.stream.SessionID, f.stream.ProjectID, "OPERON", "initial_task"); err != nil {
				t.Fatal(err)
			}
			claim, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("recovery-worker", time.Minute)
			if err != nil || !claimed {
				t.Fatalf("dispatch claim: %#v %v", claim, err)
			}
			revision := sessionRunnerRecoveryContractRevision
			if scenario == "upgrade" || scenario == "cancelled" {
				revision--
			}
			if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
				ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
				ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode, RunnerAttempt: int(f.claim.Attempt),
				CheckpointEventID: checkpoint.Sequence, WaitingFor: workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition,
				RecoveryContractRevision: revision,
			}); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "user":
				if _, _, _, err := f.repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "changed-input",
					FrameEventID: "changed-input-event", MessageUUID: "changed-input-message", Text: "Continue with the corrected input.",
				}); err != nil {
					t.Fatal(err)
				}
			case "material", "administrative":
				resumed, err := f.repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "external-evidence",
					TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
				})
				if err != nil || !resumed.Claimed {
					t.Fatalf("evidence claim: %#v %v", resumed, err)
				}
				tool := "python"
				if scenario == "administrative" {
					tool = updateStepStatusToolName
				}
				appendRunnerToolCheckpoint(t, f.repo, resumed.Claim, "new-evidence", map[string]any{
					"status": "completed", "toolPhase": "completed", "toolName": tool,
					"toolResult": map[string]any{"ok": true, "executed": true, "stdout": "new result"},
				})
			case "cancelled":
				cancelled := workspace.FrameStatusCancelled
				if _, err := f.store.UpdateFrame(f.stream.SessionID, workspace.UpdateFrameInput{Status: &cancelled}); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := f.server.wakeChangedFrameRecoveryWaits(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			current, found, err := f.store.GetCompatibilityFrameResumeDispatch(claim.ResumeEvent.ID)
			wantWake := scenario == "material" || scenario == "user" || scenario == "upgrade"
			if err != nil || !found || (current.WaitingFor == "") != wantWake || current.Attempt != claim.Attempt {
				t.Fatalf("same dispatch wake=%t: %#v %v", wantWake, current, err)
			}
			wakes := 0
			if wantWake {
				wakes = 1
			}
			assertFrameResumeDispatchEvents(t, f.store, f.stream.SessionID, map[string]int{"frame_resume_dispatch_woken": wakes})
		})
	}
}

func TestRecoveryConditionWaitProjectsPausedLiveAndAfterReload(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	if _, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "park-recovery", ReasonCode: sessionRunnerPlanStepsIncompleteReasonCode,
		Resumable: true, AutoResume: false, Destinations: []string{transcriptWebDestination},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.server.drainTranscriptWebDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	paused := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "runtime.statusChanged" && event.Payload["phase"] == "paused" {
			paused = true
		}
	}
	if !paused {
		t.Fatal("live recovery wait still appeared to be running")
	}
	frame, found, err := f.store.GetCompatibilityFrame(f.stream.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	snapshot, err := f.server.webConversationRuntimeSnapshot(frame)
	if err != nil || snapshot["state"] != "paused" || snapshot["is_processing"] != false || snapshot["can_send_message"] != true {
		t.Fatalf("reloaded recovery wait: %#v %v", snapshot, err)
	}
}

func TestRecoveryRepetitionDoesNotParkHealthyOrUpgradedWork(t *testing.T) {
	cause := sessionRunnerPlanStepsIncomplete{condition: transcriptstore.RunnerPlanCondition{
		ArtifactID: "plan", VersionID: "version", Steps: []transcriptstore.RunnerPlanConditionStep{{ID: "work", Title: "Analyze"}},
	}}.runnerCorrection()
	for _, scenario := range []string{"material", "old-runtime", "no-attempt"} {
		t.Run(scenario, func(t *testing.T) {
			state := runnerCorrectionRepetition{}
			for i := 0; i < 20; i++ {
				if scenario != "no-attempt" {
					state.observe(eventjournal.Entry{Message: eventjournal.Message{
						"type": "runner_checkpoint", "status": "completed", "toolName": "python", "toolPhase": "completed",
						"toolResult": map[string]any{"ok": scenario == "material", "executed": true},
					}})
				}
				revision := sessionRunnerRecoveryContractRevision
				if scenario == "old-runtime" {
					revision--
				}
				state.observe(eventjournal.Entry{SourceEventType: "runner_checkpoint", RuntimeProjection: cause, Message: eventjournal.Message{
					"type": "runner_checkpoint", "status": "interrupted", "reason_code": cause.ReasonCode,
					"resume_detail": cause.Detail, "recovery_contract_revision": revision,
				}})
			}
			if state.waitsForChangedCondition(cause) {
				t.Fatalf("%s incorrectly parked: %#v", scenario, state)
			}
		})
	}
}
