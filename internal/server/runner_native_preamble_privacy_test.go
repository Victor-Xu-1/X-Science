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

func TestNativePublicPreambleIncompletePresentationNeverLeaksOrBlocksValidAction(t *testing.T) {
	const safe = "I will inspect the stored fixture value."
	const hidden = "Private fixture reasoning must not be published."
	const final = "The fixture check is complete."
	for _, test := range []struct{ name, body, want string }{
		{"open-think", "<think>" + hidden, ""},
		{"partial-think", "<thi", ""},
		{"closed-think", "<think>" + hidden + "</think>" + safe, safe},
		{"open-public", `<|PublicProgressBegin|>{"version":1,"id":"open","text":"` + hidden, ""},
		{"partial-public", "<|PublicPro", ""},
		{"unmatched-control", "<|PublicProgressEnd|>" + hidden, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&map[string]any{}); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch calls.Add(1) {
				case 1:
					writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": test.body}, "")
					writeNativePreambleTool(w, `{"namespace":"fixture","key":"value"}`, "tool_calls")
				case 2:
					writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": final}, "stop")
				default:
					t.Error("optional narration created another provider request")
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(provider.Close)
			server, repo, options := newNativePreambleFixture(t, provider.URL, "private-preamble-frame")
			result, err := server.RunSessionRunnerChatOnce(context.Background(), options)
			if err != nil || result.Status != "completed" || calls.Load() != 2 {
				t.Fatalf("valid action/final blocked by optional narration: result=%+v calls=%d error=%v", result, calls.Load(), err)
			}
			progress, answer := publicationFixtureText(t, repo, options.SessionID)
			if progress != test.want || answer != final || strings.Contains(progress+answer, hidden) {
				t.Fatalf("private/control bytes escaped: progress=%q final=%q", progress, answer)
			}
			assertNativePreambleActionCounts(t, repo, options.SessionID, 1, 1)
		})
	}
}
