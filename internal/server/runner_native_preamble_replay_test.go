package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNativePublicPreambleResumeUsesExactAcceptedPrefixWithoutRepeatedAction(t *testing.T) {
	const preamble = "I will inspect the stored fixture value."
	const final = "The fixture check is complete."
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&map[string]any{}); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": preamble}, "")
			writeNativePreambleTool(w, `{"namespace":"fixture"`, "length")
		case 2:
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": preamble}, "")
			writeNativePreambleTool(w, `{"namespace":"fixture","key":"value"}`, "tool_calls")
		case 3:
			writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": final}, "stop")
		default:
			t.Error("continuation created an unexpected provider request")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(provider.Close)
	server, repo, options := newNativePreambleFixture(t, provider.URL, "resume-preamble-frame")
	first, err := server.RunSessionRunnerChatOnce(context.Background(), options)
	if err != nil || first.Status != "interrupted" || calls.Load() != 1 {
		t.Fatalf("partial proposal did not retain a resumable prefix: result=%+v calls=%d error=%v", first, calls.Load(), err)
	}
	progress, answer := publicationFixtureText(t, repo, options.SessionID)
	if progress != preamble || answer != "" {
		t.Fatalf("interruption lost or finalized prefix: progress=%q final=%q", progress, answer)
	}
	assertNativePreambleActionCounts(t, repo, options.SessionID, 0, 0)
	options.RunnerID += "-resumed"
	second, err := server.RunSessionRunnerChatOnce(context.Background(), options)
	if err != nil || second.Status != "completed" || second.Attempt != first.Attempt || calls.Load() != 3 {
		t.Fatalf("exact prefix did not resume through original tool/final path: result=%+v calls=%d error=%v", second, calls.Load(), err)
	}
	progress, answer = publicationFixtureText(t, repo, options.SessionID)
	if progress != preamble || answer != final {
		t.Fatalf("continuation replayed accepted prefix: progress=%q final=%q", progress, answer)
	}
	assertNativePreambleActionCounts(t, repo, options.SessionID, 1, 1)
}
