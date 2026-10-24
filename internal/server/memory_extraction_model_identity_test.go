package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

func TestMemoryExtractionOriginalModelPinIgnoresLaterSelectionAndRejectsProfileChange(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	registerMemoryExtractionTestProvider(t, server, "https://original-provider.example.test")
	scope := memorytools.Scope{UserID: "user-1", ProjectID: "project-1", FrameID: "frame-root", SourceFrameID: "frame-root"}
	selection := "provider:memory-extraction-test"
	profile, err := server.resolveUserModelSelectionProfileWithContext(t.Context(), scope.UserID, selection, providers.ResolutionInput{Context: t.Context(), ProjectID: scope.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	identity := memoryExtractionIdentityFromResolved(sessionRunnerResolvedModelClient{model: profile.Model, selection: selection, contextProfile: &profile,
		identity: strings.Join([]string{strings.TrimSpace(profile.Provider.ID), strings.TrimSpace(profile.Provider.Endpoint), strings.TrimSpace(profile.Model)}, "\x00")})
	if identity.Model == "" || identity.ProviderID == "" || len(identity.ProfileSHA256) != 64 {
		t.Fatalf("missing trusted identity: %+v", identity)
	}
	if _, err := server.workspaceStore.SetFrameRuntimeMetadata("frame-root", workspace.FrameRuntimeMetadata{ContextData: map[string]any{
		"web_assistant": map[string]any{"conversation_overrides": map[string]any{"model": "missing-later-selection", "model_revision": float64(99)}},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), memoryExtractionModelContextKey{}, identity)
	if _, err := server.newMemoryRuntimeModelClient(ctx, scope, "must-not-use-current-or-fallback", time.Second, 1); err != nil {
		t.Fatalf("original pinned profile replaced by later selection: %v", err)
	}
	enabled := true
	if _, err := server.workspaceStore.UpsertModelProvider(workspace.ModelProviderInput{
		ID: "memory-extraction-test", UserID: "user-1", Name: "Memory extraction test provider",
		Type: "anthropic", BaseURL: "https://changed-provider.example.test/v1", Model: memoryExtractionTestModel, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.newMemoryRuntimeModelClient(ctx, scope, identity.Model, time.Second, 1); err == nil || !strings.Contains(err.Error(), "original Memory model profile changed") {
		t.Fatalf("changed endpoint accepted: %v", err)
	}
}

func TestMemoryExtractionRecordedIdentityCannotOverwriteLaterRequest(t *testing.T) {
	runtime := newMemoryExtractionRuntimeWithFactory(nil, nil)
	first := runtime.recordLastRequest("synthetic-session", 1, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "first"}}})
	second := runtime.recordLastRequest("synthetic-session", 1, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "second"}}})
	want := workspace.MemoryExtractionModelIdentity{Model: "second-model"}
	runtime.bindRecordedModel("synthetic-session", 1, second, want)
	runtime.bindRecordedModel("synthetic-session", 1, first, workspace.MemoryExtractionModelIdentity{Model: "late-first-model"})
	if state := runtime.sessions["synthetic-session"]; state.request != second || state.model != want {
		t.Fatalf("late response replaced owned view/model: %+v", state)
	}
	third := runtime.recordLastRequest("synthetic-session", 2, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "next attempt"}}})
	runtime.bindRecordedModel("synthetic-session", 1, second, want)
	if state := runtime.sessions["synthetic-session"]; state.request != third || state.model.Model != "" {
		t.Fatalf("old attempt installed a model: %+v", state)
	}
}

type memoryIdentityFixtureClient struct {
	complete func(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error)
}

func (client memoryIdentityFixtureClient) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return client.complete(ctx, request)
}

func TestMemoryExtractionRecordingCapturesEachConcurrentCallsOwnResolvedIdentity(t *testing.T) {
	runtime := newMemoryExtractionRuntimeWithFactory(nil, nil)
	firstEntered := make(chan struct{})
	secondRecorded := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	delegate := memoryIdentityFixtureClient{complete: func(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
		name := request.Messages[0].Content
		if name == "first" {
			close(firstEntered)
			<-releaseFirst
		}
		recordMemoryExtractionCallIdentity(ctx, sessionRunnerResolvedModelClient{model: name + "-model", selection: name + "-selection", identity: name + "-profile"}, request)
		if name == "second" {
			close(secondRecorded)
			<-releaseSecond
		}
		return agentruntime.ModelResponse{Model: name + "-model"}, nil
	}}
	client := &memoryExtractionRecordingModelClient{delegate: delegate, runtime: runtime, sessionID: "fixture", attempt: 1}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.Complete(t.Context(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "first"}}})
		firstDone <- err
	}()
	<-firstEntered
	go func() {
		_, err := client.Complete(t.Context(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "second"}}})
		secondDone <- err
	}()
	<-secondRecorded
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	close(releaseSecond)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	state := runtime.sessions["fixture"]
	if state.request.Messages[0].Content != "second" || state.model.Model != "second-model" || state.model.Selection != "second-selection" {
		t.Fatalf("per-call resolved model identity crossed requests: %+v", state)
	}
}

func TestMemoryExtractionRecordingRetainsActualDynamicContinuationView(t *testing.T) {
	runtime := newMemoryExtractionRuntimeWithFactory(nil, nil)
	provider := responseLanguageFixtureModel{
		chunks:   []string{"saved-prefix continued answer"},
		response: agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "saved-prefix continued answer"}},
	}
	dynamic := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{
		client: provider, identity: "synthetic-provider\x00synthetic-endpoint\x00original-model", model: "original-model", selection: "original-selection",
	}}
	continuation := &sessionRunnerContinuationModelClient{
		delegate: dynamic, prefix: "saved-prefix", contextMessages: []agentruntime.Message{{Role: "user", Content: "synthetic continuation prefix context"}},
	}
	client := &memoryExtractionRecordingModelClient{delegate: continuation, runtime: runtime, sessionID: "continuation-fixture", attempt: 1}
	_, err := client.CompleteStream(t.Context(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "original base context"}}}, func(agentruntime.ModelStreamEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	state := runtime.sessions["continuation-fixture"]
	if state == nil || state.request == nil || len(state.request.Messages) != 2 || state.request.Messages[1].Content != "synthetic continuation prefix context" ||
		state.model.Model != "original-model" || state.model.Selection != "original-selection" {
		t.Fatalf("outer snapshot replaced actual provider continuation view: %+v", state)
	}
	raw, diagnostic := encodeMemoryForkedRequestView(state.request)
	if diagnostic != "" {
		t.Fatal(diagnostic)
	}
	restored, err := decodeMemoryForkedRequestView(raw)
	if err != nil || len(restored.Messages) != 2 || restored.Messages[1].Content != state.request.Messages[1].Content {
		t.Fatalf("persistent continuation view=%+v err=%v", restored, err)
	}
}
