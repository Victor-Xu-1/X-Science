package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/workspace"
)

func TestRunnerCompactionEpisodeDynamicSelectionRearmsBothTransports(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, change := range []string{"revision", "public_model"} {
			name := map[bool]string{false: "json", true: "stream"}[streaming] + "/" + change
			t.Run(name, func(t *testing.T) {
				var calls atomic.Int64
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ready"}}]}`))
				}))
				t.Cleanup(api.Close)
				window := 1000
				server, store, projectID, frameID := newDynamicModelTestRuntime(t, "model-a", []workspace.ModelProviderInput{
					{ID: "episode-provider-a", UserID: "dynamic-user", Name: "A", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-a", ContextWindow: &window},
					{ID: "episode-provider-b", UserID: "dynamic-user", Name: "B", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-b", ContextWindow: &window},
				})
				t.Cleanup(func() { closeTestServer(t, server) })
				snapshot, err := server.sessionConversationModelSnapshot(frameID)
				if err != nil {
					t.Fatal(err)
				}
				model := runnerCompactionModel{
					selectionKind: runnerCompactionSelectionKind(snapshot.Selection), selectionRevision: snapshot.Revision,
					providerID: "episode-provider-a", modelID: "model-a", capacity: runnerContextCapacity{Tokens: window, Source: "model_profile"},
				}
				client := newDynamicModelTestClient(server, projectID, frameID)
				binding := model.binding(server.autoCompactTokenThreshold(window))
				client.contextBudget = &sessionRunnerRequestContextBudget{server: server, episode: restoreRunnerCompactionEpisode(binding, compactionEpisodeReplayFixture(binding))}
				request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("a", 4000)}}}
				call := func() error {
					if streaming {
						_, err := client.CompleteStream(t.Context(), request, nil)
						return err
					}
					_, err := client.Complete(t.Context(), request)
					return err
				}
				if err := call(); err != nil || calls.Load() != 1 {
					t.Fatalf("same fresh binding: calls=%d err=%v", calls.Load(), err)
				}
				selection := "model-a"
				if change == "public_model" {
					selection = "model-b"
				}
				if _, err := store.SetCompatibilityConversationModel(frameID, selection); err != nil {
					t.Fatal(err)
				}
				var pressure *sessionRunnerRequestContextPressureError
				if err := call(); !errors.As(err, &pressure) || pressure.threshold != 800 || calls.Load() != 1 {
					t.Fatalf("changed same-window binding bypassed admission: calls=%d err=%v", calls.Load(), err)
				}
			})
		}
	}
}

func TestRunnerCompactionEpisodePreOutputHandoffRearmsAtSameWindow(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[streaming], func(t *testing.T) {
			var calls atomic.Int64
			var switchModel func()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				switchModel()
				http.Error(w, "controlled provider failure", http.StatusUnauthorized)
			}))
			t.Cleanup(api.Close)
			window := 1000
			server, store, projectID, frameID := newDynamicModelTestRuntime(t, "model-a", []workspace.ModelProviderInput{
				{ID: "episode-handoff-a", UserID: "dynamic-user", Name: "A", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-a", ContextWindow: &window},
				{ID: "episode-handoff-b", UserID: "dynamic-user", Name: "B", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-b", ContextWindow: &window},
			})
			t.Cleanup(func() { closeTestServer(t, server) })
			switchModel = func() {
				if _, err := store.SetCompatibilityConversationModel(frameID, "model-b"); err != nil {
					t.Error(err)
				}
			}
			snapshot, err := server.sessionConversationModelSnapshot(frameID)
			if err != nil {
				t.Fatal(err)
			}
			model := runnerCompactionModel{
				selectionKind: runnerCompactionSelectionKind(snapshot.Selection), selectionRevision: snapshot.Revision,
				providerID: "episode-handoff-a", modelID: "model-a", capacity: runnerContextCapacity{Tokens: window, Source: "model_profile"},
			}
			client := newDynamicModelTestClient(server, projectID, frameID)
			binding := model.binding(server.autoCompactTokenThreshold(window))
			client.contextBudget = &sessionRunnerRequestContextBudget{server: server, episode: restoreRunnerCompactionEpisode(binding, compactionEpisodeReplayFixture(binding))}
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("a", 4000)}}}
			if streaming {
				_, err = client.CompleteStream(t.Context(), request, nil)
			} else {
				_, err = client.Complete(t.Context(), request)
			}
			var pressure *sessionRunnerRequestContextPressureError
			if !errors.As(err, &pressure) || pressure.threshold != 800 || calls.Load() != 1 {
				t.Fatalf("handoff reused prior model's compact episode: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}
