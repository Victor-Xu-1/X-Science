package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"x-science/internal/persistence/workspace"
	"x-science/internal/tools/registry"
)

func assertOriginalSkillContextAtProvider(t *testing.T, fixture *kernelSkillDraftFixture, run *sessionRunnerChatRun, name, want string, absent ...string) {
	t.Helper()
	fixture.server.tools = registry.Default()
	enabled := true
	if _, err := fixture.store.CreateAgent(workspace.CreateAgentInput{
		ID: "original-context-owner", UserID: fixture.stream.OwnerID, Name: "OPERON",
		DisplayName: "Original context fixture", Enabled: &enabled, Unrestricted: true, SkillNames: []string{name},
		Description: "Owns one bounded synthetic context request.", SystemPrompt: "ORIGINAL_CONTEXT_OWNER_FIXTURE",
	}); err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	session, err := fixture.server.loadTranscriptFrameSessionProjection(fixture.stream)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools    []any `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var system strings.Builder
		for _, message := range request.Messages {
			if message.Role == "system" {
				system.WriteString(message.Content)
			}
		}
		if hasChatToolNamed(request.Tools, "skill") {
			select {
			case observed <- system.String():
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"The bounded original context request was observed."},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	options := normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
		SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID,
		Endpoint: provider.URL + "/v1/chat/completions", Model: "original-context-fixture",
		AllowedTools: []string{"skill"}, DisableMCPDiscovery: true, DisableSkillDiscovery: true,
		SelectedSkillNames: []string{name}, RequestTimeout: 5 * time.Second, PreparationTimeout: 20 * time.Second,
		MaxToolRounds: 1, MaxAttempts: 1, ReplayLimit: 20, OutputLimitBytes: 64 << 10,
	})
	_, runnerErr := fixture.server.runSessionRunnerChat(ctx, options, session, entries, run)
	select {
	case system := <-observed:
		if !strings.Contains(system, "ORIGINAL_CONTEXT_OWNER_FIXTURE") || strings.Count(system, want) != 1 {
			t.Fatalf("actual provider request did not contain exactly one original scoped body: count=%d err=%v", strings.Count(system, want), runnerErr)
		}
		for _, text := range absent {
			if strings.Contains(system, text) {
				t.Fatalf("actual provider request included competing or unresolved context %q", text)
			}
		}
	default:
		t.Fatalf("real runner preparation never reached the controlled provider: %v", runnerErr)
	}
}
