package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	workspace "synon-go/internal/persistence/workspace"
)

func TestWebContextUsageLiveStreamThroughProviderAndSQLite(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"abcdefghijklmnop\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":4,\"total_tokens\":21}}\n\ndata: [DONE]\n\n"))
	}))
	defer provider.Close()
	// Release before waiting for provider.Close if an assertion fails midstream.
	defer unblock()
	enabled := true
	srv, _, project, frame := newDynamicModelTestRuntime(t, "stream-model", []workspace.ModelProviderInput{{ID: "stream", UserID: "dynamic-user", Name: "Stream", Type: "openai-compatible", BaseURL: provider.URL + "/v1", Model: "stream-model", Enabled: &enabled}})
	client := newDynamicModelTestClient(srv, project, frame)
	client.contextUsage = newSessionContextUsageRecorder(srv, frame, 1, SessionRunnerChatOptions{RuntimeSessionConfig: map[string]any{"contextWindow": 1000}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "stream test"}}}, nil)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		api := p3JSONRequest(t, srv, http.MethodGet, "/api/conversations/"+frame+"/context-usage", nil, "dynamic-user")
		payload := p3DecodeObject(t, api)
		snapshot, _ := payload["snapshot"].(map[string]any)
		progress, _ := snapshot["progress"].(map[string]any)
		if progress != nil {
			policy, _ := payload["autoCompaction"].(map[string]any)
			if snapshot["source"] != "estimated" || snapshot["state"] != "request" || progress["outputTokens"] != float64(4) || policy["thresholdTokens"] != float64(800) {
				t.Fatalf("invalid running projection: %+v", payload)
			}
			observed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		t.Fatal("no stream projection before provider completion")
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not finish")
	}
	api := p3JSONRequest(t, srv, http.MethodGet, "/api/conversations/"+frame+"/context-usage", nil, "dynamic-user")
	snapshot := p3DecodeObject(t, api)["snapshot"].(map[string]any)
	if snapshot["source"] != "provider" || snapshot["usedTokens"] != float64(21) || snapshot["progress"] != nil {
		t.Fatalf("provider reconciliation failed: %+v", snapshot)
	}
}
