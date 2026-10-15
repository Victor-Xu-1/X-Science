package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestSemanticCompletionPublishesOneTruthfulAnswer(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	const frameID = "semantic-completion-frame"
	seedTranscriptWebFrame(t, store, "local", "semantic-project", frameID)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "semantic-message", ClientMessageID: "semantic-input",
		Text: "Explain web_search without executing it.",
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, server, "local", frameID)
	const answer = "This is an explanation of search capability. No tool operation was executed."
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		content := answer
		if strings.Contains(string(mustJSON(t, request.Messages)), "Write a brief expert orientation before the task begins.") {
			content = "I will explain the supplied question."
		} else {
			requests.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}}})
	}))
	defer provider.Close()
	result, err := server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: frameID, RunnerID: "semantic-runner", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", AllowedTools: []string{"read_file"},
		LeaseTTL: time.Minute, MaxAttempts: 1, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	})
	if err != nil || result.Status != "completed" || requests.Load() != 1 {
		t.Fatalf("semantic inference reopened work: result=%#v requests=%d error=%v", result, requests.Load(), err)
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", frameID)
	if err != nil || !found {
		t.Fatal(err)
	}
	events, err := repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	answers, terminal, answerIndex, terminalIndex := 0, 0, -1, -1
	for index, event := range events {
		payload := mustTranscriptPayloadObject(t, event.ResolvedPayloadJSON)
		if event.Event.Type == "assistant_message" {
			answers++
			answerIndex = index
			text := transcriptPayloadText(payload)
			if !strings.HasPrefix(text, answer) || strings.Count(text, "Execution details are unverified") != 1 {
				t.Fatalf("truthful primary answer or single disclosure lost: %q", text)
			}
		}
		if event.Event.Type == "runner_finished" {
			terminal++
			terminalIndex = index
		}
		if event.Event.Type == "runner_checkpoint" && stringValue(payload["toolCallId"]) != "" {
			t.Fatal("a prose mention caused tool execution")
		}
	}
	if answers != 1 || terminal != 1 || answerIndex < 0 || answerIndex+1 != terminalIndex {
		t.Fatalf("final publication identity/order: answers=%d terminals=%d answer=%d terminal=%d", answers, terminal, answerIndex, terminalIndex)
	}
}
