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

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerTypedProgressNeverPromotesTrailingPrivateToolDraft(t *testing.T) {
	for _, mode := range []string{"single", "multiple", "legacy", "final", "interrupted"} {
		t.Run(mode, func(t *testing.T) { testRunnerProgressPublicationAuthority(t, mode) })
	}
}

func testRunnerProgressPublicationAuthority(t *testing.T, mode string) {
	t.Helper()
	const first = "I will inspect the stored fixture value."
	const next = "I will then compare the available fields."
	const private = "This ordinary trailing draft is not an authorized public update."
	const final = "The fixture check is complete."
	chunks := []string{earlyProgressEnvelope("first", first), private}
	wantProgress := first
	if mode == "multiple" {
		chunks = append(chunks, earlyProgressEnvelope("next", next), private)
		wantProgress += next
	} else if mode == "legacy" {
		chunks = []string{first}
	} else if mode == "final" {
		chunks = []string{earlyProgressEnvelope("first", first), final}
	}
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		sequence := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch sequence {
		case 1:
			for _, chunk := range chunks {
				writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": chunk}, "")
			}
			if mode == "interrupted" {
				return // No terminal response or tool admission; the accepted block survives.
			}
			if mode == "final" {
				writePublicationFixtureEvent(w, map[string]any{}, "stop")
			} else {
				writePublicationFixtureEvent(w, map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "fixture-read", "type": "function",
					"function": map[string]any{"name": "runtime_get", "arguments": `{"namespace":"fixture","key":"value"}`},
				}}}, "tool_calls")
			}
		case 2:
			messages, ok := request["messages"].([]any)
			if !ok || !hasToolResultMessage(messages, requireProviderToolCallID(t, request, "runtime_get", 0), "") {
				t.Error("native tool did not reach the ordinary execution/result path")
			}
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": final}, "stop")
		default:
			t.Error("unexpected extra provider request")
			http.Error(w, "unexpected request", http.StatusConflict)
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer provider.Close()
	store, repo, _ := newTranscriptWebFixture(t)
	const frame = "public-authority-frame"
	seedTranscriptWebFrame(t, store, "local", "public-authority-project", frame)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frame, MessageUUID: "public-authority-message", ClientMessageID: "public-authority-input",
		Text: "Give a short fixture summary using the available records.",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: frame, RunnerID: "public-authority-runner", Endpoint: provider.URL,
		Model: "fixture-model", AllowedTools: []string{"runtime_get"}, MaxAttempts: 1, MaxToolRounds: 2,
		RequestTimeout: 5 * time.Second, LeaseTTL: time.Minute, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	})
	wantStatus, wantRequests, wantFinal := "completed", int64(2), final
	if mode == "final" {
		wantRequests = 1
	}
	if mode == "interrupted" {
		wantStatus, wantRequests, wantFinal = "interrupted", 1, ""
	}
	if err != nil || result.Status != wantStatus || requests.Load() != wantRequests {
		t.Fatalf("result=%+v requests=%d err=%v", result, requests.Load(), err)
	}
	progress, answer := publicationFixtureText(t, repo, frame)
	if progress != wantProgress || answer != wantFinal || strings.Contains(progress+answer, private) {
		t.Fatalf("public progress=%q final=%q; want progress=%q final=%q", progress, answer, wantProgress, wantFinal)
	}
}

func writePublicationFixtureEvent(w http.ResponseWriter, delta map[string]any, finish string) {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	encoded, _ := json.Marshal(map[string]any{"id": "publication-fixture", "choices": []any{choice}})
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	w.(http.Flusher).Flush()
}

func publicationFixtureText(t *testing.T, repo *transcriptstore.Repository, frame string) (string, string) {
	t.Helper()
	ctx := context.Background()
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", frame)
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	events, err := repo.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var progress, final strings.Builder
	for _, event := range events {
		if event.Event.Type != "content_delta" && event.Event.Type != "assistant_message" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Event.Type == "content_delta" {
			progress.WriteString(stringValue(payload["text"]))
		} else {
			final.WriteString(stringValue(payload["text"]))
		}
	}
	return progress.String(), final.String()
}
