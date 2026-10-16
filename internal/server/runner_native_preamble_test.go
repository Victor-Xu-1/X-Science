package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// The barrier holds a genuine SSE response before its arguments or terminal
// reason are complete. Public narration and executable action have different
// boundaries; neither an in-memory callback nor a completed response proves it.
func TestNativePublicPreamblePersistsBeforeCompleteToolResponse(t *testing.T) {
	const preamble = "I will inspect the stored fixture value."
	const final = "The fixture check is complete."
	const private = "Private fixture reasoning must never become visible."
	const frame = "native-preamble-frame"
	var calls atomic.Int64
	reached := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			if !nativePreambleToolAdvertised(t, request) {
				return
			}
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "reasoning_content": private}, "")
			writePublicationFixtureEvent(w, map[string]any{"content": preamble}, "")
			writePublicationFixtureEvent(w, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "native-read", "type": "function",
				"function": map[string]any{"name": "search_skills", "arguments": `{"query":"fi`},
			}}}, "")
			close(reached)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			writePublicationFixtureEvent(w, map[string]any{"content": "This later native draft is not part of the accepted preamble."}, "")
			writePublicationFixtureEvent(w, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `xture"}`},
			}}}, "tool_calls")
		case 2:
			messages, ok := request["messages"].([]any)
			if !ok || !hasToolResultMessage(messages, requireProviderToolCallID(t, request, "search_skills", 0), "") {
				t.Error("original native tool did not reach the ordinary result path")
			}
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": final}, "stop")
		default:
			t.Error("unexpected provider or presentation request")
			http.Error(w, "unexpected request", http.StatusConflict)
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(provider.Close)
	server, repo, options := newNativePreambleFixture(t, provider.URL, frame)
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result SessionRunnerCycleResult
		err    error
	}
	finished := make(chan outcome, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		if !joined {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("owned runner did not join after cancellation")
			}
		}
	})
	go func() {
		result, err := server.RunSessionRunnerChatOnce(ctx, options)
		finished <- outcome{result, err}
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE provider did not reach the held incomplete-arguments boundary")
	}
	deadline := time.Now().Add(time.Second)
	var progress, answer string
	for {
		progress, answer = publicationFixtureText(t, repo, frame)
		if progress != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond) // Observe durable state, never release the SSE barrier.
	}
	if progress != preamble || answer != "" {
		t.Fatalf("closed native preamble was not durable before complete tool response: progress=%q final=%q", progress, answer)
	}
	page := transcriptToolHistoryPage(t, server, "/api/conversations/"+frame+"/messages?limit=100", "")
	items, _ := page["items"].([]any)
	visible := 0
	for _, raw := range items {
		message, _ := raw.(map[string]any)
		if message["type"] != "text" || message["position"] != "left" {
			continue
		}
		assertTranscriptTextMessage(t, message, preamble)
		visible++
	}
	if visible != 1 {
		t.Fatalf("public history did not expose exactly one native preamble: count=%d", visible)
	}
	assertNativePreambleActionCounts(t, repo, frame, 0, 0)
	select {
	case early := <-finished:
		joined = true
		t.Fatalf("runner settled before complete arguments: result=%+v error=%v", early.result, early.err)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case result := <-finished:
		joined = true
		if result.err != nil || result.result.Status != "completed" || calls.Load() != 2 {
			t.Fatalf("ordinary tool/final settlement: result=%+v calls=%d error=%v", result.result, calls.Load(), result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("complete tool response did not settle")
	}
	progress, answer = publicationFixtureText(t, repo, frame)
	if progress != preamble || answer != final || strings.Contains(progress+answer, private) {
		t.Fatalf("settlement replayed or leaked content: progress=%q final=%q", progress, answer)
	}
	assertNativePreambleActionCounts(t, repo, frame, 1, 1)
}

func newNativePreambleFixture(t *testing.T, endpoint, frame string) (*Server, *transcriptstore.Repository, SessionRunnerChatOptions) {
	t.Helper()
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", frame+"-project", frame)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frame, MessageUUID: frame + "-message", ClientMessageID: frame + "-input",
		Text: "Give a short fixture summary using the available records.",
	}); err != nil {
		t.Fatal(err)
	}
	return server, repo, SessionRunnerChatOptions{
		SessionID: frame, RunnerID: frame + "-runner", Endpoint: endpoint,
		Model: "fixture-model", AllowedTools: []string{"search_skills"}, MaxAttempts: 1, MaxToolRounds: 2,
		RequestTimeout: 5 * time.Second, LeaseTTL: time.Minute, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	}
}

func writeNativePreambleTool(w http.ResponseWriter, arguments, finish string) {
	writePublicationFixtureEvent(w, map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": "native-read", "type": "function",
		"function": map[string]any{"name": "search_skills", "arguments": arguments},
	}}}, finish)
}

func assertNativePreambleActionCounts(t *testing.T, repo *transcriptstore.Repository, frame string, wantStarted, wantCompleted int) {
	t.Helper()
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", frame)
	if err != nil || !found {
		t.Fatalf("stream found=%t error=%v", found, err)
	}
	events, err := repo.ListProjectedCoordinateEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, Limit: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, completed := 0, 0
	for _, event := range events {
		if event.Event.Type == "content_reset" {
			t.Fatal("native progress required a public content reset")
		}
		if event.Event.Type != "runner_checkpoint" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if stringValue(payload["toolName"]) != "search_skills" {
			continue
		}
		switch stringValue(payload["toolPhase"]) {
		case "start":
			started++
		case "completed":
			completed++
		}
	}
	if started != wantStarted || completed != wantCompleted {
		t.Fatalf("tool execution counts started=%d completed=%d; want %d/%d", started, completed, wantStarted, wantCompleted)
	}
}

func nativePreambleToolAdvertised(t *testing.T, request map[string]any) bool {
	t.Helper()
	tools, _ := request["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		if function["name"] == "search_skills" {
			return true
		}
	}
	t.Error("canonical fixture tool is not in the actual provider advertisement")
	return false
}

func TestNativePreambleCanonicalToolFixtureExecutesOrdinaryCompleteAction(t *testing.T) {
	const preamble = "I will inspect the stored fixture value."
	const final = "The fixture check is complete."
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			if !nativePreambleToolAdvertised(t, request) {
				return
			}
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": preamble}, "")
			writeNativePreambleTool(w, `{"query":"fixture"}`, "tool_calls")
		} else {
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": final}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(provider.Close)
	server, repo, options := newNativePreambleFixture(t, provider.URL, "complete-preamble-frame")
	result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
	if err != nil || result.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("ordinary advertised fixture action failed: result=%+v calls=%d error=%v", result, calls.Load(), err)
	}
	progress, answer := publicationFixtureText(t, repo, options.SessionID)
	if progress != preamble || answer != final {
		t.Fatalf("ordinary complete response changed: progress=%q final=%q", progress, answer)
	}
	assertNativePreambleActionCounts(t, repo, options.SessionID, 1, 1)
}
