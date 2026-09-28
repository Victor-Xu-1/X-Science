package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"synon-go/internal/agentruntime"
	"synon-go/internal/skills"
	"testing"
)

func TestLiveLoadedContractSurvivesResultExternalization(t *testing.T) {
	var calls atomic.Int32
	var tailVisible atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"load-live-contract\",\"type\":\"function\",\"function\":{\"name\":\"skill\",\"arguments\":\"{\\\"skill\\\":\\\"large-workflow\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		for _, message := range request.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, "ALTERNATIVE_ROUTE_AT_END") {
				tailVisible.Store(true)
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Contract inspected.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	fixture := newAgentSaveArtifactsFixture(t)
	ctx, _ := appendLargeToolResultSource(t, fixture, "load-live-contract", "skill")
	catalog := skills.NewCatalog()
	catalog.AddSkill(skills.Skill{Name: "large-workflow", Path: "builtin:large-workflow", Body: strings.Repeat("Ordinary documented contract details.\n", 600) + "ALTERNATIVE_ROUTE_AT_END"})
	fixture.server.skillCatalog = catalog
	engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{SessionID: fixture.stream.SessionID, AllowedTools: []string{"skill"}, OutputLimitBytes: 50000, Endpoint: provider.URL, Model: "controlled-protocol"})
	var durable string
	engine.OnEventError = func(event agentruntime.Event) error {
		if event.Type == agentruntime.EventToolCompleted {
			durable = event.Result
		}
		return nil
	}
	_, err := engine.Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Inspect the complete workflow contract."}}, Tools: []agentruntime.ToolSchema{{Name: "skill"}}, MaxToolRounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	var descriptor agentruntime.LargeToolResultDescriptor
	if err := json.Unmarshal([]byte(durable), &descriptor); err != nil || !descriptor.Truncated {
		t.Fatalf("fixture did not externalize: %v %.200s", err, durable)
	}
	if !tailVisible.Load() {
		t.Fatal("newly loaded contract tail was lost before the next model request; only a preview reached the model")
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected extra model calls: %d", calls.Load())
	}
}
