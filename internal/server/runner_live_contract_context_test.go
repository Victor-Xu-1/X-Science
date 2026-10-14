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
	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

func TestLiveLoadedContractSurvivesResultExternalization(t *testing.T) {
	var calls atomic.Int32
	var tailVisible atomic.Bool
	var canonicalCallID atomic.Value
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatCompletionRequest
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
		callID, _ := canonicalCallID.Load().(string)
		var assistantPaired bool
		for _, message := range request.Messages {
			for _, call := range message.ToolCalls {
				if message.Role == "assistant" && call.ID == callID && call.Function.Name == "skill" {
					assistantPaired = true
				}
			}
			if message.Role == "tool" && message.ToolCallID == callID && strings.Contains(message.Content, "ALTERNATIVE_ROUTE_AT_END") {
				tailVisible.Store(true)
			}
		}
		if callID == "" || !assistantPaired || !tailVisible.Load() {
			t.Errorf("loaded contract was not paired with its canonical call: id=%q assistant=%t tail=%t", callID, assistantPaired, tailVisible.Load())
			http.Error(w, "missing canonical contract pair", http.StatusConflict)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Contract inspected.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{
		SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), ClaimToken: fixture.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim},
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	catalog := skills.NewCatalog()
	catalog.AddSkill(skills.Skill{Name: "large-workflow", Path: "builtin:large-workflow", Body: strings.Repeat("Ordinary documented contract details.\n", 600) + "ALTERNATIVE_ROUTE_AT_END"})
	fixture.server.skillCatalog = catalog
	options := SessionRunnerChatOptions{SessionID: fixture.stream.SessionID, RunnerID: fixture.claim.RunnerID, AllowedTools: []string{"skill"}, OutputLimitBytes: 50000, Endpoint: provider.URL, Model: "controlled-protocol"}
	engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, options)
	var durable string
	engine.OnEventError = func(event agentruntime.Event) error {
		if event.Type == agentruntime.EventModelResponse && len(event.ToolCalls) > 0 {
			if len(event.ToolCalls) != 1 || event.ToolCalls[0].Name != "skill" || event.ToolCalls[0].ID == "load-live-contract" {
				return fmt.Errorf("unexpected canonical skill proposal: %#v", event.ToolCalls)
			}
			canonicalCallID.Store(event.ToolCalls[0].ID)
			return fixture.server.checkpointChatModelToolCallsWithContext(ctx, options, run, event.ToolCalls)
		}
		if err := fixture.server.checkpointSessionRunnerToolEvent(ctx, options, run, event); err != nil {
			return err
		}
		if event.Type == agentruntime.EventToolCompleted {
			if event.ToolCallID != canonicalCallID.Load() {
				return fmt.Errorf("completed contract call %q does not match its proposal", event.ToolCallID)
			}
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
	callID, _ := canonicalCallID.Load().(string)
	record, found, err := fixture.store.FindRunnerLargeToolResult(ctx, descriptor.ArtifactID, callID, "skill")
	if err != nil || !found || record.SourceEventID <= 0 || record.SourceEventID != run.ToolSourceEventIDs[callID] ||
		record.VersionID != descriptor.VersionID || record.ContentSHA256 != descriptor.SHA256 {
		t.Fatalf("externalized contract lost its exact durable source: id=%q source=%d found=%t error=%v", callID, record.SourceEventID, found, err)
	}
	if !tailVisible.Load() {
		t.Fatal("newly loaded contract tail was lost before the next model request; only a preview reached the model")
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected extra model calls: %d", calls.Load())
	}
}
