package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"synon-go/internal/agentruntime"
	workspace "synon-go/internal/persistence/workspace"
	"testing"
)

func TestContextCapacityDynamicSelectionBindsAdmissionAndTelemetryPerCall(t *testing.T) {
	var calls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":1200,"completion_tokens":10,"total_tokens":1210}}`))
	}))
	defer api.Close()
	large, small, output := 10000, 1000, 2048
	srv, store, projectID, frameID := newDynamicModelTestRuntime(t, "model-a", []workspace.ModelProviderInput{
		{ID: "capacity-a", UserID: "dynamic-user", Name: "A", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-a", ContextWindow: &large},
		{ID: "capacity-b", UserID: "dynamic-user", Name: "B", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-b", ContextWindow: &small},
		{ID: "capacity-u", UserID: "dynamic-user", Name: "Unknown", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-u", MaxTokens: &output},
	})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	client := newDynamicModelTestClient(srv, projectID, frameID)
	client.contextUsage = newSessionContextUsageRecorder(srv, frameID, 1, SessionRunnerChatOptions{})
	client.contextBudget = &sessionRunnerRequestContextBudget{server: srv, armed: true}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("a", 4000)}}}
	if _, err := client.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := readContextUsageTest(t, srv.runtimeStore, frameID); got.Model != "model-a" || got.LimitTokens != large || got.LimitSource != "model_profile" || got.UsedTokens != 1210 {
		t.Fatalf("first resolved model: %+v", got)
	}
	if _, err := store.SetCompatibilityConversationModel(frameID, "model-b"); err != nil {
		t.Fatal(err)
	}
	var pressure *sessionRunnerRequestContextPressureError
	if _, err := client.CompleteStream(context.Background(), request, nil); !errors.As(err, &pressure) || pressure.threshold != 800 {
		t.Fatalf("new model admission ignored: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("over-budget switched model was dispatched")
	}
	if got := readContextUsageTest(t, srv.runtimeStore, frameID); got.Model != "model-b" || got.LimitTokens != small || got.Progress == nil || got.Progress.Phase != "compacting" {
		t.Fatalf("pressure snapshot stale: %+v", got)
	}
	if _, err := store.SetCompatibilityConversationModel(frameID, "model-u"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompleteStream(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	if got := readContextUsageTest(t, srv.runtimeStore, frameID); got.Model != "model-u" || got.LimitTokens != 0 || got.LimitSource != "unknown" || got.Source != "provider" {
		t.Fatalf("unknown model reused previous capacity: %+v", got)
	}
}

func TestContextCapacityPreOutputHandoffRebindsBeforeBothTransports(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			var calls atomic.Int64
			var switchModel func()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switchModel()
				http.Error(w, "first provider unavailable", http.StatusUnauthorized)
			}))
			defer api.Close()
			large, small := 10000, 1000
			srv, store, projectID, frameID := newDynamicModelTestRuntime(t, "model-a", []workspace.ModelProviderInput{
				{ID: "handoff-capacity-a", UserID: "dynamic-user", Name: "A", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-a", ContextWindow: &large},
				{ID: "handoff-capacity-b", UserID: "dynamic-user", Name: "B", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-b", ContextWindow: &small},
			})
			t.Cleanup(func() { _ = srv.Close(context.Background()) })
			switchModel = func() {
				if _, err := store.SetCompatibilityConversationModel(frameID, "model-b"); err != nil {
					t.Error(err)
				}
			}
			client := newDynamicModelTestClient(srv, projectID, frameID)
			client.contextUsage = newSessionContextUsageRecorder(srv, frameID, 1, SessionRunnerChatOptions{})
			client.contextBudget = &sessionRunnerRequestContextBudget{server: srv, armed: true}
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("a", 4000)}}}
			var err error
			if streaming {
				_, err = client.CompleteStream(context.Background(), request, nil)
			} else {
				_, err = client.Complete(context.Background(), request)
			}
			var pressure *sessionRunnerRequestContextPressureError
			if !errors.As(err, &pressure) || pressure.threshold != 800 || calls.Load() != 1 {
				t.Fatalf("handoff bypassed new capacity: calls=%d err=%v", calls.Load(), err)
			}
			if got := readContextUsageTest(t, srv.runtimeStore, frameID); got.Model != "model-b" || got.LimitTokens != small {
				t.Fatalf("handoff usage stale: %+v", got)
			}
		})
	}
}
