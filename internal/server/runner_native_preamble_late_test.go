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
)

func TestNativePreambleObservationDoesNotDiscardSafeCompletedLateNarration(t *testing.T) {
	const safe = "I will inspect the stored fixture value."
	const comparison = "p < 0.05; I will inspect the records."
	for _, test := range []struct{ name, before, after, want string }{
		{"comparison", comparison, "", comparison},
		{"later-private-close", "<think>private fixture", "</think>" + safe, safe},
		{"oversized-private-prefix", "<think>" + strings.Repeat("private ", 2500), "</think>" + safe, safe},
	} {
		t.Run(test.name, func(t *testing.T) {
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
					writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": test.before}, "")
					writeNativePreambleTool(w, `{"query":"fi`, "")
					if test.after != "" {
						writePublicationFixtureEvent(w, map[string]any{"content": test.after}, "")
					}
					writePublicationFixtureEvent(w, map[string]any{"tool_calls": []any{map[string]any{
						"index": 0, "function": map[string]any{"arguments": `xture"}`},
					}}}, "tool_calls")
				} else {
					writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": "The fixture check is complete."}, "stop")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(provider.Close)
			server, repo, options := newNativePreambleFixture(t, provider.URL, "late-preamble-frame")
			result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
			if err != nil || result.Status != "completed" || calls.Load() != 2 {
				t.Fatalf("late valid narration changed action path: result=%+v calls=%d error=%v", result, calls.Load(), err)
			}
			progress, final := publicationFixtureText(t, repo, options.SessionID)
			if progress != test.want || final != "The fixture check is complete." {
				t.Fatalf("nonaccepted prefix erased late narration: progress=%q want=%q final=%q", progress, test.want, final)
			}
			assertNativePreambleActionCounts(t, repo, options.SessionID, 1, 1)
		})
	}
}
