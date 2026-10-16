package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

// Exercise the actual provider decoder, dynamic main-agent recorder, temporary
// SQLite store and authenticated read API. A successful model response with a
// partial usage object is legal; absent prompt usage must not become measured 0.
func TestWebContextUsagePartialProviderUsageDoesNotClaimMeasuredTotal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{
				Role: "user", Content: strings.Repeat("abcd", 256),
			}}}
			rows, _, err := estimateRunnerRequestUsage(request)
			if err != nil {
				t.Fatal(err)
			}
			inputEstimate := 0
			for _, row := range rows {
				inputEstimate += row.Tokens
			}
			if inputEstimate <= 10 {
				t.Fatal("fixture must distinguish estimated input from the output-only receipt")
			}
			snapshot := readProviderUsagePresenceFixture(t, stream, `{"completion_tokens":10}`, request)
			if snapshot["source"] != "estimated" || snapshot["usedTokens"] != float64(inputEstimate+10) ||
				snapshot["outputTokens"] != float64(10) || snapshot["state"] != "complete" {
				t.Fatalf("partial provider receipt was mislabeled as a measured context total: inputEstimate=%d snapshot=%+v", inputEstimate, snapshot)
			}
		})
	}
}

func readProviderUsagePresenceFixture(t *testing.T, stream bool, usageJSON string, request agentruntime.ModelRequest) map[string]any {
	t.Helper()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var received struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil || len(received.Messages) != len(request.Messages) {
			t.Errorf("legal request was not decoded: messages=%d err=%v", len(received.Messages), err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		usage := ""
		if usageJSON != "" {
			usage = `,"usage":` + usageJSON
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]%s}\n\ndata: [DONE]\n\n", usage)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]%s}`, usage)
	}))
	defer provider.Close()
	enabled := true
	srv, _, project, frame := newDynamicModelTestRuntime(t, "usage-presence-model", []workspace.ModelProviderInput{{
		ID: "usage-presence", UserID: "dynamic-user", Name: "Usage presence", Type: "openai-compatible",
		BaseURL: provider.URL + "/v1", Model: "usage-presence-model", Enabled: &enabled,
	}})
	client := newDynamicModelTestClient(srv, project, frame)
	client.contextUsage = newSessionContextUsageRecorder(srv, frame, 1, SessionRunnerChatOptions{})
	var err error
	if stream {
		_, err = client.CompleteStream(context.Background(), request, nil)
	} else {
		_, err = client.Complete(context.Background(), request)
	}
	if err != nil || calls != 1 {
		t.Fatalf("model must succeed exactly once before telemetry assertion: calls=%d err=%v", calls, err)
	}
	api := p3JSONRequest(t, srv, http.MethodGet, "/api/conversations/"+frame+"/context-usage", nil, "dynamic-user")
	if api.Code != http.StatusOK || api.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("context API HTTP=%d cache=%q body=%s", api.Code, api.Header().Get("Cache-Control"), api.Body.String())
	}
	payload := p3DecodeObject(t, api)
	snapshot, ok := payload["snapshot"].(map[string]any)
	if payload["status"] != "available" || !ok {
		t.Fatalf("context API did not return a valid persisted snapshot: %+v", payload)
	}
	return snapshot
}
