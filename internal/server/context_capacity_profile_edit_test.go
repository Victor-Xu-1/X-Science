package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"synon-go/internal/agentruntime"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/providers"
	"testing"
)

func TestContextCapacitySelectedProfileEditInvalidatesPreparedClient(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("over-budget provider dispatched")
		http.Error(w, "forbidden", 500)
	}))
	defer api.Close()
	oldWindow, currentWindow := 10000, 1000
	srv, store, project, frame := newDynamicModelTestRuntime(t, "model-a", []workspace.ModelProviderInput{{ID: "edited-profile", UserID: "dynamic-user", Name: "Edited", Type: "custom", BaseURL: api.URL + "/v1", Model: "model-a", ContextWindow: &currentWindow}})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	provider, found, err := store.GetModelProvider("dynamic-user", "edited-profile")
	if err != nil || !found {
		t.Fatal(err)
	}
	profile, err := providers.BuildModelProfile(provider, nil, "dynamic-user", providers.ResolutionInput{ProjectID: project, MaxAttempts: 1, MaxResponseBytes: 64 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	profile.ContextWindow = &oldWindow
	selection, err := srv.sessionConversationModelSnapshot(frame)
	if err != nil {
		t.Fatal(err)
	}
	prepared := &requestBudgetForbiddenModel{}
	client := newDynamicModelTestClient(srv, project, frame)
	client.initial = sessionRunnerResolvedModelClient{client: prepared, model: "model-a", contextProfile: &profile}
	client.initialReady, client.initialSelection, client.initialRevision = true, selection.Selection, selection.Revision
	client.contextBudget = &sessionRunnerRequestContextBudget{server: srv, armed: true}
	client.contextUsage = newSessionContextUsageRecorder(srv, frame, 1, SessionRunnerChatOptions{})
	_, err = client.Complete(context.Background(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("a", 4000)}}})
	var pressure *sessionRunnerRequestContextPressureError
	if !errors.As(err, &pressure) || pressure.threshold != 800 || prepared.calls != 0 {
		t.Fatalf("stale prepared capacity used: calls=%d err=%v", prepared.calls, err)
	}
	if got := readContextUsageTest(t, srv.runtimeStore, frame); got.LimitTokens != currentWindow || got.LimitSource != "model_profile" {
		t.Fatalf("profile edit not reflected: %+v", got)
	}
}
