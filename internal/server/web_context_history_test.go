package server

import (
	"net/http"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
)

func TestWebContextHistoryRetainsPerRequestWindowInsteadOfOnlyLatest(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	project := createP3Project(t, store, "context-history", "local")
	created := p3JSONRequest(t, app, http.MethodPost, "/api/conversations", map[string]any{
		"name": "Context history", "assistant": map[string]any{"id": "synonbiomed:OPERON"},
		"extra": map[string]any{"project_id": project.ID},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create HTTP %d", created.Code)
	}
	id := webString(p3DecodeObject(t, created)["id"])
	recorder := newSessionContextUsageRecorder(app, id, 1, SessionRunnerChatOptions{})
	for _, used := range []int{900, 100} {
		snapshot := recorder.begin("model", agentruntime.ModelRequest{
			Messages: []agentruntime.Message{{Role: "user", Content: "private input must not be persisted"}},
		})
		if snapshot == nil {
			t.Fatal("missing request snapshot")
		}
		recorder.finish(snapshot, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: used - 10, OutputTokens: 10, TotalTokens: used}}, nil)
	}
	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("history HTTP %d: %s", response.Code, response.Body.String())
	}
	payload := p3DecodeObject(t, response)
	history, _ := payload["history"].(map[string]any)
	samples, _ := history["samples"].([]any)
	if len(samples) != 2 || history["totalObserved"] != float64(2) {
		t.Fatalf("per-request windows lost: %+v", history)
	}
	if samples[0].(map[string]any)["usedTokens"] != float64(900) || samples[1].(map[string]any)["usedTokens"] != float64(100) {
		t.Fatalf("window trend does not preserve real per-call usage: %+v", samples)
	}
	if strings.Contains(response.Body.String(), "private input") {
		t.Fatal("context telemetry exposed prompt content")
	}
	foreign := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+id+"/context-usage", nil, "foreign-user")
	if foreign.Code != http.StatusForbidden && foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign history access HTTP %d", foreign.Code)
	}
}
