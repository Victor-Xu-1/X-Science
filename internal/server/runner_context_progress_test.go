package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
)

type contextProgressModel struct{ observe func() }

func (m contextProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{}, nil
}

func TestContextProgressPublishesToolBoundaryPhaseInsideThrottle(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "phase-transition", attempt: 1, limit: 1000, limitSource: "configured"}
	snapshot := recorder.begin("model", agentruntime.ModelRequest{})
	tracker := recorder.streamProgress(snapshot)
	now := snapshot.ObservedAt
	tracker.now = func() time.Time { return now }
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true})
	first := readContextUsageTest(t, store, "phase-transition")
	if first.Progress == nil || first.Progress.Phase != "thinking" {
		t.Fatalf("initial private activity did not publish thinking: %+v", first.Progress)
	}

	// Native providers send this boundary only once. The remaining argument
	// fragments need not emit any other activity before the response finishes.
	now = now.Add(50 * time.Millisecond)
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary})
	got := readContextUsageTest(t, store, "phase-transition")
	if got.State != "request" || got.Progress == nil || got.Progress.Phase != "generating" {
		t.Fatalf("tool boundary phase was throttled before provider finish: state=%s progress=%+v", got.State, got.Progress)
	}
	if !got.Progress.ObservedAt.Equal(now.UTC()) || got.Progress.OutputTokens != 0 || got.Progress.UsedTokens != snapshot.UsedTokens {
		t.Fatalf("tool boundary changed public token counts or lost its observed time: %+v", got.Progress)
	}
}

func TestContextProgressThrottleFailureAndLateSampleFences(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "fences", attempt: 1, limit: 1000, limitSource: "configured"}
	snapshot := recorder.begin("model", agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "input"}}})
	tracker := recorder.streamProgress(snapshot)
	now := time.Now()
	tracker.now = func() time.Time { return now }
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{ContentDelta: "abcd"})
	first, _, _ := store.Get(contextUsageNamespace("fences"), "latest")
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{ContentDelta: "甲乙"})
	still, _, _ := store.Get(contextUsageNamespace("fences"), "latest")
	if first.Version != still.Version {
		t.Fatal("unthrottled write")
	}
	now = now.Add(time.Second)
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{ReasoningActive: true})
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true, ContentDelta: "not public text"})
	got := readContextUsageTest(t, store, "fences")
	if got.Progress == nil || got.Progress.OutputTokens != 3 || got.Progress.Phase != "thinking" {
		t.Fatalf("numeric progress=%+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "abcd") || strings.Contains(string(raw), "甲乙") {
		t.Fatal("text stored in telemetry")
	}
	late := *snapshot
	tracker.finish(agentruntime.ModelResponse{}, errors.New("interrupted"))
	if err := recorder.persistProgress(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	got = readContextUsageTest(t, store, "fences")
	if got.State != "failed" || got.Progress == nil || got.Progress.OutputTokens != 3 {
		t.Fatalf("lost partial evidence: %+v", got)
	}
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{ContentDelta: "late text"})
	if readContextUsageTest(t, store, "fences").State != "failed" {
		t.Fatal("late stream reopened request")
	}
	next := recorder.begin("next", agentruntime.ModelRequest{})
	_ = recorder.persist(*snapshot, true)
	if err := recorder.persistProgress(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	if readContextUsageTest(t, store, "fences").RequestID != next.RequestID {
		t.Fatal("late previous request overwrote next")
	}
	next.Progress = &runnerContextProgress{Phase: "thinking", ObservedAt: now, UsedTokens: next.UsedTokens}
	recorder.frameExists = func(context.Context) (bool, error) { return false, nil }
	if err := recorder.persistProgress(t.Context(), *next); err != nil {
		t.Fatal(err)
	}
	if readContextUsageTest(t, store, "fences").Progress != nil {
		t.Fatal("late progress wrote after frame deletion")
	}
	if _, err := store.Delete(contextUsageNamespace("fences"), "latest"); err != nil {
		t.Fatal(err)
	}
	recorder.frameExists = nil
	if err := recorder.persistProgress(t.Context(), *next); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(contextUsageNamespace("fences"), "latest"); err != nil || found {
		t.Fatalf("live progress recreated absent request: found=%v err=%v", found, err)
	}
}

func TestContextProgressPressureUsesExistingGuardAndSkipsCancellation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
		t.Cleanup(func() { _ = store.Close() })
		recorder := &sessionContextUsageRecorder{store: store, sessionID: "pressure", attempt: 1, limit: 1000, limitSource: "configured"}
		client := &sessionRunnerDynamicModelClient{contextUsage: recorder, initialReady: true,
			contextBudget: newRequestContextBudgetFixture(nil, 800, true)}
		request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "assistant", Content: strings.Repeat("a", (800-7)*4)}}}
		call := func(ctx context.Context) error {
			if streaming {
				_, err := client.CompleteStream(ctx, request, nil)
				return err
			}
			_, err := client.Complete(ctx, request)
			return err
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(call(cancelled), context.Canceled) {
			t.Fatal("cancellation changed")
		}
		if _, found, _ := store.Get(contextUsageNamespace("pressure"), "latest"); found {
			t.Fatal("cancelled call created progress")
		}
		var pressure *sessionRunnerRequestContextPressureError
		if !errors.As(call(context.Background()), &pressure) {
			t.Fatal("existing budget guard lost")
		}
		got := readContextUsageTest(t, store, "pressure")
		if client.initialReady || got.UsedTokens != 800 || got.Progress == nil || got.Progress.Phase != "compacting" || got.Progress.OutputTokens != 0 {
			t.Fatalf("pressure was not visible before dispatch: %+v", got)
		}
		if !validRunnerContextProgress(got) {
			t.Fatal("pressure snapshot invalid")
		}
	}
}

func TestContextCompactionPolicySharesEightyPercentBoundary(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	for _, window := range []int{1000, 128000, 1000000} {
		policy, err := srv.contextCompactionPolicy(window)
		if err != nil || !policy.Enabled || policy.ThresholdTokens != window*80/100 || policy.Percent != 80 || policy.Source != "window_percent" {
			t.Fatalf("policy=%+v err=%v", policy, err)
		}
		if policy.ThresholdTokens != srv.autoCompactTokenThreshold(window) {
			t.Fatal("UI and request boundary diverged")
		}
	}
	policy, _ := srv.contextCompactionPolicy(1000)
	for _, tokens := range []int{799, 800} {
		request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "assistant", Content: strings.Repeat("a", (tokens-7)*4)}}}
		budget := newRequestContextBudgetFixture(nil, policy.ThresholdTokens, true)
		err := budget.beforeCall(context.Background(), request)
		if (err != nil) != (tokens == 800) {
			t.Fatalf("at %d tokens: %v", tokens, err)
		}
	}
	_, _ = srv.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), 300)
	policy, _ = srv.contextCompactionPolicy(1000)
	if policy.ThresholdTokens != 300 || policy.Percent != 30 || policy.Source != "token_override" {
		t.Fatalf("override hidden: %+v", policy)
	}
	_, _ = srv.settingsStore.Set(configStoreKey("autoCompactEnabled"), false)
	policy, _ = srv.contextCompactionPolicy(1000)
	if policy.Enabled {
		t.Fatal("disabled policy changed")
	}
}
func (m contextProgressModel) CompleteStream(_ context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if err := emit(agentruntime.ModelStreamEvent{ContentDelta: "abcdefghijklmnop"}); err != nil {
		return agentruntime.ModelResponse{}, err
	}
	m.observe()
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "abcdefghijklmnop"}, Usage: agentruntime.ModelUsage{InputTokens: 20, OutputTokens: 4, TotalTokens: 24}}, nil
}

func TestContextUsagePublishesStreamBeforeProviderFinishes(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "streaming", attempt: 1, limit: 1000, limitSource: "configured"}
	model := contextProgressModel{observe: func() {
		entry, found, err := store.Get(contextUsageNamespace("streaming"), "latest")
		if err != nil || !found {
			t.Fatalf("usage missing: %v", err)
		}
		raw, _ := json.Marshal(entry.Value)
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		progress, ok := value["progress"].(map[string]any)
		if !ok || progress["phase"] != "generating" || progress["outputTokens"] != float64(4) {
			t.Errorf("stream progress not available before completion: %+v", value)
		}
	}}
	client := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "stream-model"}, contextUsage: recorder}
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "inspect"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	final := readContextUsageTest(t, store, "streaming")
	if final.Source != "provider" || final.UsedTokens != 24 {
		t.Fatalf("provider final lost: %+v", final)
	}
}

func TestContextProgressDoesNotBlockStreamOrCancellationOnBusyRuntimeStore(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "busy-progress", attempt: 1, limit: 1000, limitSource: "configured"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	providerStarted := make(chan struct{})
	allowFirstEvent := make(chan struct{})
	providerReturned := make(chan struct{})
	firstDelivered := make(chan struct{})
	secondDelivered := make(chan struct{})
	model := communicationTimingModel{run: func(ctx context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		defer close(providerReturned)
		close(providerStarted) // Request observation has already been written.
		select {
		case <-allowFirstEvent:
		case <-ctx.Done():
			return agentruntime.ModelResponse{}, ctx.Err()
		}
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		if err := ctx.Err(); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "public text"}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		<-ctx.Done()
		return agentruntime.ModelResponse{}, ctx.Err()
	}}
	client := &sessionRunnerDynamicModelClient{
		initialReady: true, initial: sessionRunnerResolvedModelClient{client: model, model: "busy-progress-model"},
		contextUsage: recorder,
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
			switch event.Kind {
			case agentruntime.ModelStreamEventPrivateReasoning:
				close(firstDelivered)
			case agentruntime.ModelStreamEventContentDelta:
				close(secondDelivered)
			}
			return nil
		})
		callDone <- err
	}()
	defer func() {
		cancel()
		select {
		case err := <-callDone:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("stream error after releasing runtime KV writer = %v, want cancellation", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("stream did not exit after releasing runtime KV writer")
		}
	}()

	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start after request observation")
	}

	// Hold the real RuntimeKV store lock after the model request begins. The
	// first activity callback reaches the downstream consumer before telemetry.
	err := store.EditNamespace("busy-progress-fixture", func(map[string]runtimekv.Entry) (bool, error) {
		close(allowFirstEvent)
		select {
		case <-firstDelivered:
		case <-time.After(time.Second):
			t.Error("first activity callback did not reach its downstream consumer")
			return false, nil
		}
		select {
		case <-secondDelivered:
		case <-time.After(time.Second):
			t.Error("optional context-progress persistence blocked the next provider event")
		}
		cancel()
		select {
		case <-providerReturned:
		case <-time.After(time.Second):
			t.Error("optional context-progress persistence blocked the provider's cancellation check")
		}
		return false, nil // Release before waiting for terminal telemetry or closing the store.
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestContextProgressBacksOffAcrossPhaseChangesAfterBusyWrite(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "backoff", attempt: 1, limit: 1000, limitSource: "configured"}
	snapshot := recorder.begin("model", agentruntime.ModelRequest{})
	tracker := recorder.streamProgress(snapshot)
	now := snapshot.ObservedAt
	tracker.now = func() time.Time { return now }
	frameChecks := 0
	recorder.frameExists = func(ctx context.Context) (bool, error) {
		frameChecks++
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Error("live frame read was not bounded")
		}
		if frameChecks == 1 {
			return false, context.DeadlineExceeded
		}
		return true, nil
	}
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true})
	now = now.Add(50 * time.Millisecond)
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "abcd"})
	if frameChecks != 1 || readContextUsageTest(t, store, "backoff").Progress != nil {
		t.Fatal("a phase change retried a busy optional write before its interval")
	}
	now = now.Add(contextProgressInterval)
	tracker.observe(t.Context(), agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary})
	got := readContextUsageTest(t, store, "backoff")
	if frameChecks != 2 || got.Progress == nil || got.Progress.OutputTokens != 1 {
		t.Fatalf("optional progress did not recover or lost sampled activity: checks=%d progress=%+v", frameChecks, got.Progress)
	}
}
