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
	transcriptstore "synon-go/internal/persistence/transcript"
)

// Exercise the actual assembled provider request, not a fixture containing only
// journal prose. Runtime instructions and exposed function definitions consume
// the same context window as conversation history.
func TestTranscriptRunnerCompactsFullRequestBeforeProviderCall(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-request-budget", "frame-request-budget")
	var requests atomic.Int64
	var baseTokens atomic.Int64
	var threshold atomic.Int64
	var historyTokens atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		actual := agentruntime.ModelRequest{Messages: agentRuntimeMessagesFromChat(request.Messages)}
		for _, tool := range request.Tools {
			actual.Tools = append(actual.Tools, agentruntime.ToolSchema{
				Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.Parameters,
			})
		}
		rows, _, err := estimateRunnerRequestUsage(actual)
		if err != nil {
			t.Errorf("estimate actual request: %v", err)
			http.Error(w, "invalid schema", http.StatusBadRequest)
			return
		}
		var total int
		for _, row := range rows {
			total += row.Tokens
		}
		content := "current inspection complete"
		if requests.Add(1) == 1 {
			baseTokens.Store(int64(total))
			content = strings.Repeat("archived inspection detail ", maxInt(total/20, 256))
			historyTokens.Store(int64(estimateTextTokens(content)))
		} else {
			compacted := false
			for _, message := range request.Messages {
				if strings.Contains(message.Content, "Synon compact handoff context:") {
					compacted = true
				}
			}
			if !compacted || int64(total) >= threshold.Load() {
				t.Errorf("full request was not reduced before provider call: compacted=%t tokens=%d threshold=%d base=%d history=%d",
					compacted, total, threshold.Load(), baseTokens.Load(), historyTokens.Load())
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, content)
	}))
	defer provider.Close()
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	options := SessionRunnerChatOptions{
		SessionID: "frame-request-budget", RunnerID: "request-budget-runner",
		Endpoint: provider.URL + "/v1/chat/completions", APIKey: "test-key", Model: "test-model",
		LeaseTTL: time.Minute, MaxAttempts: 1, ReplayLimit: 100, DisableSkillDiscovery: true,
	}
	for index, input := range []string{"record the earlier inspection", "summarize the current inspection"} {
		if index == 1 {
			// The replay alone is below this threshold, but the full request is
			// above it. Derive the budget from the real runtime, not fixed prompt
			// lengths that become stale when unrelated tool contracts evolve.
			threshold.Store(baseTokens.Load() + historyTokens.Load()/2)
			if historyTokens.Load() >= threshold.Load() {
				t.Fatal("fixture does not isolate request overhead from replay size")
			}
			if _, err := server.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), float64(threshold.Load())); err != nil {
				t.Fatal(err)
			}
		}
		id := fmt.Sprintf("input-request-budget-%d", index)
		if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
			FrameID: options.SessionID, MessageUUID: id, ClientMessageID: id, Text: input,
		}); err != nil {
			t.Fatal(err)
		}
		result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "interrupted" {
			resumeOptions := options
			resumeOptions.SessionID = ""
			result, err = server.RunSessionRunnerChatOnce(context.Background(), resumeOptions)
		}
		if err != nil || result.Status != "completed" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("provider requests=%d, want one per user input", requests.Load())
	}
	if entries, err := server.eventJournal.ReadAll(options.SessionID); err != nil || len(entries) != 0 {
		t.Fatalf("transcript compaction wrote legacy history: entries=%d err=%v", len(entries), err)
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", options.SessionID)
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	events, err := repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: "local", ThroughPublicationSequence: stream.NextPublication - 1, Limit: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	interruptions, compactions, terminals := 0, 0, 0
	for _, event := range events {
		if event.Event.Type == "runner_finished" {
			terminals++
		}
		if event.Event.Type != "runner_checkpoint" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["reason_code"] == sessionRunnerRequestContextPressureReasonCode {
			interruptions++
		}
		if payload["toolPhase"] == "auto_compact" && payload["reason"] == sessionRunnerRequestContextPressureReasonCode {
			compactions++
		}
	}
	if interruptions != 1 || compactions != 1 || terminals != 2 {
		t.Fatalf("interruptions=%d compactions=%d terminals=%d", interruptions, compactions, terminals)
	}
}
