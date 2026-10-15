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

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestSemanticReviewResumeUsesCurrentChecksWithoutForcedTools(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	seedAnsweredTaskIntake(t, f.server, f.stream.OwnerID, f.stream.SessionID)
	if _, found, err := f.repo.EnsureActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID); err != nil || !found {
		t.Fatalf("task intent: found=%t error=%v", found, err)
	}
	cause := (sessionRunnerCompletionReviewCorrection{
		Summary: "explicit user-required tool evidence is incomplete",
		Issues: []sessionRunnerReviewIssue{{MessageIndex: 0, Verdict: "fail", Severity: "high",
			Claim:    "the explicitly requested real MCP method execution has no successful MCP tool result",
			Evidence: "the explicitly requested real MCP method execution has no successful MCP tool result"}},
	}).runnerCorrection()
	interrupted, err := f.repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: f.claim, ClientMessageID: "prior-semantic-review", ReasonCode: cause.ReasonCode,
		ResumeDetail: cause.Detail, Cause: &cause, Resumable: true, AutoResume: true,
		RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("persist prior observation: %#v %v", interrupted, err)
	}
	var lock sync.Mutex
	var observed []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if strings.Contains(string(mustJSON(t, request["messages"])), "Write a brief expert orientation before the task begins.") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "I will inspect the supplied task."},
			}}})
			return
		}
		lock.Lock()
		observed = append(observed, request)
		lock.Unlock()
		// Stop at the real resumed provider boundary; no fabricated answer or
		// successful tool receipt is supplied merely to retire the old gate.
		http.Error(w, "controlled provider boundary stop", http.StatusBadRequest)
	}))
	defer provider.Close()
	result, runErr := f.server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: f.stream.SessionID, RunnerID: "semantic-resume", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", AllowedTools: []string{"read_file", "ask_user"},
		LeaseTTL: time.Minute, MaxAttempts: 1, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
		TranscriptResumeSource: transcriptstore.ResumeSourceCheckpoint, TranscriptCheckpoint: interrupted.Checkpoint.Sequence,
	})
	lock.Lock()
	defer lock.Unlock()
	if len(observed) == 0 {
		t.Fatalf("resumed provider boundary absent: %#v %v", result, runErr)
	}
	for _, request := range observed {
		if request["tool_choice"] == "required" || strings.Contains(string(mustJSON(t, request["messages"])), sessionRunnerDurableCorrectionContextMarker) {
			t.Fatal("old semantic finding forced another action")
		}
	}
	if runErr == nil && result.Status == "completed" {
		t.Fatal("retiring a semantic condition fabricated task completion")
	}
	if cause.Condition == nil || cause.Condition.Review == nil || cause.Condition.Review.Summary != "explicit user-required tool evidence is incomplete" {
		t.Fatal("historic review evidence was rewritten")
	}
}
