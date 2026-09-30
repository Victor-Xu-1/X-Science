package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestRecoveredAutonomousPlanLegacyRequestDoesNotForceProviderToolChoice(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	seedAnsweredTaskIntake(t, f.server, f.stream.OwnerID, f.stream.SessionID)
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical task intent: found=%t error=%v", found, err)
	}
	// Preserve the source-only legacy start, not an invented completed model
	// round. The host did persist its real plan before recording the condition.
	planContext, planRun := appendLargeToolResultSource(t, f, "prior-navigation", generatePlanToolName)
	planRun.TaskIntent, planRun.TaskIntentID, planRun.TaskIntentRevision = intent.Text, intent.ID, intent.Revision
	planRun.AutonomousPlanning = true
	planInput := revisionPlanInput("Explain supplied result")
	if _, err := f.server.executeAgentGeneratePlan(planContext, f.stream.SessionID, "prior-navigation", planInput); err != nil {
		t.Fatal(err)
	}
	remaining, err := f.server.incompleteGeneratedPlanCondition(f.stream.SessionID)
	if err != nil || remaining == nil {
		t.Fatalf("durable plan: %#v %v", remaining, err)
	}
	cause := remaining.runnerCorrection()
	interrupted, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "prior-plan-correction", ReasonCode: cause.ReasonCode,
		ResumeDetail: cause.Detail, Cause: &cause, Resumable: true, AutoResume: true,
		RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("persist prior host condition: %#v %v", interrupted, err)
	}
	var lock sync.Mutex
	var observed []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if strings.Contains(string(mustJSON(t, body["messages"])), "Write a brief expert orientation before the task begins.") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "I will inspect the supplied task."},
			}}})
			return
		}
		lock.Lock()
		observed = append(observed, body)
		lock.Unlock()
		// Deliberately stop at the real provider boundary. This test verifies
		// restored request policy, not completion of the artifact task.
		http.Error(w, "controlled provider boundary stop", http.StatusBadRequest)
	}))
	defer provider.Close()
	result, runErr := f.server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: f.stream.SessionID, RunnerID: "policy-resume", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", AllowedTools: []string{generatePlanToolName, updateStepStatusToolName},
		LeaseTTL: time.Minute, MaxAttempts: 1, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
		TranscriptResumeSource: transcriptstore.ResumeSourceCheckpoint, TranscriptCheckpoint: interrupted.Checkpoint.Sequence,
	})
	lock.Lock()
	defer lock.Unlock()
	if len(observed) == 0 {
		t.Fatalf("real resumed provider request was not observed: %#v %v", result, runErr)
	}
	for _, request := range observed {
		if request["tool_choice"] == "required" {
			metadata, _, _ := f.store.GetFrameRuntimeMetadata(f.stream.SessionID)
			intent, _, _ := f.repo.GetActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
			run := &sessionRunnerChatRun{TaskIntent: intent.Text, TaskIntentID: intent.ID, TaskIntentRevision: intent.Revision}
			advisory, policyErr := f.server.recoveredCompletionCorrectionIsAdvisory(f.stream.SessionID, run,
				recoveredRunnerCorrection{ReasonCode: cause.ReasonCode, Detail: cause.Detail, Condition: cause.Condition})
			t.Fatalf("obsolete automatic-plan condition forced another tool call: choice=%#v advisory=%t policy_error=%v mode=%v approved=%v binding_match=%t",
				request["tool_choice"], advisory, policyErr, metadata.ContextData["_plan_control_mode"], metadata.ContextData["_plan_approved"], sessionRunnerPlanMatchesTask(metadata.ContextData, run))
		}
		if strings.Contains(string(mustJSON(t, request["messages"])), sessionRunnerDurableCorrectionContextMarker) {
			t.Fatal("obsolete automatic-plan condition remained in resumed model context")
		}
	}
	if runErr == nil && result.Status == "completed" {
		t.Fatal("controlled provider failure was misreported as task completion")
	}
}
