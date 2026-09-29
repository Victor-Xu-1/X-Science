package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"synon-go/internal/agentruntime"
	sessionstore "synon-go/internal/persistence/sessions"
	"synon-go/internal/providers"
)

// Exercise the environment-configured factory used when no saved profile is
// selected, with a real provider protocol and the server's durable state store.
func TestStaticOutputBudgetSurvivesModelRecreation(t *testing.T) {
	var budgets []int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		budgets = append(budgets, request.MaxTokens)
		w.Header().Set("Content-Type", "application/json")
		if request.MaxTokens <= 40 {
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"draft"}}],"usage":{"prompt_tokens":100,"completion_tokens":40}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"complete"}}]}`))
	}))
	defer api.Close()
	srv, _, project, frame := newDynamicModelTestRuntime(t, "", nil)
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	options := SessionRunnerChatOptions{SessionID: frame, Endpoint: api.URL, Model: "test-model", MaxAttempts: 1}
	options, err := srv.resolveSessionRunnerModelAuthority(context.Background(), sessionstore.Session{ID: frame, Project: &sessionstore.Project{ID: project}}, options, 1)
	if err != nil || options.ModelProfile != nil || options.modelOwnerUserID != "dynamic-user" {
		t.Fatalf("default model lost task ownership: owner=%q err=%v", options.modelOwnerUserID, err)
	}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "preserve the complete task"}}}
	makeClient := func() agentruntime.StreamingModelClient {
		engine := srv.newAgentRuntimeEngineWithContext(context.Background(), options)
		return engine.Model.(agentruntime.StreamingModelClient)
	}
	if _, err := makeClient().CompleteStream(context.Background(), request, func(agentruntime.ModelStreamEvent) error { return nil }); !providers.IsProviderOutputTokenLimit(err) {
		t.Fatalf("first generation must preserve the provider stop: %v", err)
	}
	response, err := makeClient().CompleteStream(context.Background(), request, func(agentruntime.ModelStreamEvent) error { return nil })
	if err != nil || response.Message.Content != "complete" || !reflect.DeepEqual(budgets, []int{0, 80}) {
		t.Fatalf("static provider did not adapt: budgets=%v response=%#v err=%v", budgets, response, err)
	}
	// A per-request explicit cap remains authoritative after adaptation.
	request.MaxTokens = 20
	_, err = makeClient().CompleteStream(context.Background(), request, func(agentruntime.ModelStreamEvent) error { return nil })
	if !providers.IsProviderOutputTokenLimit(err) || budgets[len(budgets)-1] != 20 {
		t.Fatalf("explicit cap overridden: budgets=%v err=%v", budgets, err)
	}
}
