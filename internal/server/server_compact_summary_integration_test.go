package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolsAPICompactSummaryFramingBoundary(t *testing.T) {
	const internalMarker = "SYNTHETIC_INTERNAL_MARKER"
	for _, test := range []struct {
		name, content, summary, status, summaryError string
	}{
		{
			name:         "ambiguous-framing",
			content:      "<analysis><summary>" + internalMarker + "</summary></analysis>",
			status:       "deterministic-context",
			summaryError: "compact model returned an empty summary",
		},
		{
			name:    "valid-framing",
			content: "<analysis>" + strings.Repeat("K", 19) + internalMarker + "</analysis>\n<summary>PUBLIC</summary>",
			summary: "PUBLIC",
			status:  "model-generated",
		},
		{
			name:    "legacy-plain-text",
			content: "K legacy summary.",
			summary: "K legacy summary.",
			status:  "model-generated",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int64
			modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected compact request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				var request struct {
					Model string            `json:"model"`
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode compact request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				if request.Model != "compact-boundary-model" || len(request.Tools) != 0 {
					t.Error("compact request used an unexpected model or advertised tools")
					http.Error(w, "unexpected request body", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{
						"role": "assistant", "content": test.content,
					}}},
				}); err != nil {
					t.Errorf("encode compact response: %v", err)
				}
			}))
			t.Cleanup(modelAPI.Close)

			srv := New(Options{
				FileRoot: t.TempDir(),
				CompactSummarizer: SessionRunnerChatOptions{
					Endpoint: modelAPI.URL + "/v1/chat/completions", Model: "compact-boundary-model",
					RequestTimeout: time.Second, MaxAttempts: 1,
				},
			})
			t.Cleanup(func() { closeTestServer(t, srv) })
			api := httptestServer(t, srv)
			const sessionID = "compact-summary-boundary"
			const instructions = "Preserve the current request and completed work."
			postToolInput(t, api.URL, "session_store", map[string]any{
				"sessionId": sessionID, "title": "Compact summary boundary",
			})
			for _, role := range []string{"user", "assistant"} {
				postToolInput(t, api.URL, "session_append", map[string]any{
					"sessionId": sessionID, "role": role, "clientMessageId": "compact-boundary-" + role,
					"message": map[string]any{"type": "message", "text": "Preserve the " + role + " context."},
				})
			}
			session, found, err := srv.sessionStore.Get(sessionID)
			if err != nil || !found {
				t.Fatalf("load compact session: found=%t err=%v", found, err)
			}
			entries, err := srv.eventJournal.ReadAfter(sessionID, 0, 20)
			if err != nil || len(entries) != 2 {
				t.Fatalf("load compact input: entries=%d err=%v", len(entries), err)
			}
			deterministic, err := buildCompactModelContextSummary(session, entries, instructions, "manual")
			if err != nil {
				t.Fatalf("build deterministic compact summary: %v", err)
			}
			wantSummary := test.summary
			if test.summaryError != "" {
				wantSummary = deterministic
			}
			object := func(value any) map[string]any {
				t.Helper()
				result, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("expected JSON object, got %T", value)
				}
				return result
			}
			checkSummary := func(label string, value map[string]any) {
				t.Helper()
				if value["summary"] != wantSummary || value["summaryStatus"] != test.status {
					t.Fatalf("%s did not preserve the expected summary and status", label)
				}
				if object(value["modelContext"])["summary"] != wantSummary {
					t.Fatalf("%s model context disagrees with its summary", label)
				}
				metadata := object(value["modelSummary"])
				if metadata["attempted"] != true {
					t.Fatalf("%s did not attempt the configured provider", label)
				}
				if test.summaryError != "" {
					if metadata["error"] != test.summaryError {
						t.Fatalf("%s did not retain the fixed summary error", label)
					}
				} else if _, exists := metadata["error"]; exists || metadata["source"] != "model" {
					t.Fatalf("%s did not accept the model summary without an error", label)
				}
			}
			body := postToolInput(t, api.URL, "Compact", map[string]any{
				"sessionId": sessionID, "instructions": instructions, "trigger": "manual", "limit": float64(20),
			})
			result := object(body["result"])
			checkpoint := object(result["checkpoint"])
			checkSummary("returned checkpoint", checkpoint)
			returnedEvent := object(result["event"])
			if returnedEvent["eventId"] == nil || checkpoint["compactEventId"] != returnedEvent["eventId"] {
				t.Fatal("returned checkpoint has no matching compact event identity")
			}
			checkSummary("returned event", object(returnedEvent["message"]))
			stored := postToolInput(t, api.URL, "runtime_get", map[string]any{"namespace": "compact", "key": sessionID})
			storedValue := object(object(object(stored["result"])["entry"])["value"])
			if !reflect.DeepEqual(storedValue, checkpoint) {
				t.Fatal("persisted compact checkpoint differs from the returned checkpoint")
			}
			replayed := postToolInput(t, api.URL, "session_replay", map[string]any{"sessionId": sessionID, "limit": float64(20)})
			replayEntries, ok := object(replayed["result"])["entries"].([]any)
			if !ok {
				t.Fatal("session replay did not return entries")
			}
			compactEvents := 0
			for _, rawEntry := range replayEntries {
				entry := object(rawEntry)
				message := object(entry["message"])
				if message["type"] != "session_compact" {
					continue
				}
				compactEvents++
				checkSummary("persisted event", message)
				if entry["eventId"] != returnedEvent["eventId"] || !reflect.DeepEqual(message["modelSummary"], checkpoint["modelSummary"]) {
					t.Fatal("persisted compact event identity or model metadata differs from the returned result")
				}
			}
			if compactEvents != 1 || requests.Load() != 1 {
				t.Fatalf("compact events=%d provider requests=%d, want one each", compactEvents, requests.Load())
			}
			for _, value := range []any{body, stored, replayed} {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatalf("encode compact result for boundary check: %v", err)
				}
				if strings.Contains(string(raw), internalMarker) {
					t.Fatal("compact result exposed the synthetic internal marker")
				}
			}
		})
	}
}
