package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	"synon-go/internal/persistence/runtimekv"
)

type contextProgressModel struct{ observe func() }

func (m contextProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{}, nil
}

func TestContextProgressThrottleFailureAndLateSampleFences(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "fences", attempt: 1, limit: 1000, limitSource: "configured"}
	snapshot := recorder.begin("model", agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "input"}}})
	tracker := recorder.streamProgress(snapshot)
	now := time.Now()
	tracker.now = func() time.Time { return now }
	tracker.observe(agentruntime.ModelStreamEvent{ContentDelta: "abcd"})
	first, _, _ := store.Get(contextUsageNamespace("fences"), "latest")
	tracker.observe(agentruntime.ModelStreamEvent{ContentDelta: "甲乙"})
	still, _, _ := store.Get(contextUsageNamespace("fences"), "latest")
	if first.Version != still.Version {
		t.Fatal("unthrottled write")
	}
	now = now.Add(time.Second)
	tracker.observe(agentruntime.ModelStreamEvent{ReasoningActive: true})
	tracker.observe(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true, ContentDelta: "not public text"})
	got := readContextUsageTest(t, store, "fences")
	if got.Progress == nil || got.Progress.OutputTokens != 3 || got.Progress.Phase != "thinking" {
		t.Fatalf("numeric progress=%+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "abcd") || strings.Contains(string(raw), "甲乙") {
		t.Fatal("text stored in telemetry")
	}
	tracker.finish(agentruntime.ModelResponse{}, errors.New("interrupted"))
	got = readContextUsageTest(t, store, "fences")
	if got.State != "failed" || got.Progress == nil || got.Progress.OutputTokens != 3 {
		t.Fatalf("lost partial evidence: %+v", got)
	}
	tracker.observe(agentruntime.ModelStreamEvent{ContentDelta: "late text"})
	if readContextUsageTest(t, store, "fences").State != "failed" {
		t.Fatal("late stream reopened request")
	}
	next := recorder.begin("next", agentruntime.ModelRequest{})
	_ = recorder.persist(*snapshot, true)
	if readContextUsageTest(t, store, "fences").RequestID != next.RequestID {
		t.Fatal("late previous request overwrote next")
	}
}

func TestContextProgressPressureUsesExistingGuardAndSkipsCancellation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
		t.Cleanup(func() { _ = store.Close() })
		recorder := &sessionContextUsageRecorder{store: store, sessionID: "pressure", attempt: 1, limit: 1000, limitSource: "configured"}
		client := &sessionRunnerDynamicModelClient{contextUsage: recorder, initialReady: true,
			contextBudget: &sessionRunnerRequestContextBudget{threshold: 800, armed: true}}
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
		budget := &sessionRunnerRequestContextBudget{threshold: policy.ThresholdTokens, armed: true}
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
