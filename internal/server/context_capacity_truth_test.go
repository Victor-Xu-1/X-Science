package server

import (
	"context"
	"synon-go/internal/agentruntime"
	"synon-go/internal/persistence/runtimekv"
	"synon-go/internal/providers"
	"testing"
)

func TestContextCapacityUnknownDoesNotInventWindowOrCompactionThreshold(t *testing.T) {
	if got := runnerContextWindow(SessionRunnerChatOptions{}); got != 0 {
		t.Fatalf("unconfigured model capacity = %d, want unknown (0)", got)
	}
	threshold, source := resolveContextCompactionThreshold(0, nil, false)
	if threshold != 0 || source != "unknown" {
		t.Fatalf("unknown model compaction = %d/%s, want 0/unknown", threshold, source)
	}
	threshold, source = resolveContextCompactionThreshold(0, 32000, true)
	if threshold != 32000 || source != "token_override" {
		t.Fatalf("explicit independent token budget = %d/%s", threshold, source)
	}
	if got, _ := resolveContextCompactionThreshold(128000, 1000000, true); got != 128000 {
		t.Fatalf("explicit budget enlarged model capacity: %d", got)
	}
}

func TestContextCapacityProfileAndSessionBudgetHaveOneAuthority(t *testing.T) {
	window, output := 128000, 2048
	profile := &providers.ModelProfile{Model: "alias", ContextWindow: &window, MaxTokens: &output}
	if got := contextCapacityForModel(profile, SessionRunnerChatOptions{}); got.Tokens != window || got.Source != "model_profile" {
		t.Fatalf("profile capacity: %+v", got)
	}
	for _, budget := range []int{32000, 256000} {
		got := contextCapacityForModel(profile, SessionRunnerChatOptions{RuntimeSessionConfig: map[string]any{"contextWindow": budget}})
		if got.Tokens != min(budget, window) {
			t.Fatalf("session budget enlarged model capacity: %+v", got)
		}
	}
	profile.ContextWindow = nil
	if got := contextCapacityForModel(profile, SessionRunnerChatOptions{}); got.Tokens != 0 || got.Source != "unknown" {
		t.Fatalf("output/alias inferred context: %+v", got)
	}
}

func TestContextCapacityRetiresLegacyDefaultWithoutChangingReceipt(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	recorder := newSessionContextUsageRecorder(srv, "frame-capacity", 1, SessionRunnerChatOptions{})
	snapshot := recorder.begin("routed-alias", agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "test"}}})
	if snapshot == nil {
		t.Fatal("no usage record")
	}
	recorder.finish(snapshot, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: 51764, OutputTokens: 969, TotalTokens: 52733}}, nil)
	legacy := *snapshot
	legacy.LimitTokens, legacy.LimitSource = 1000000, "runner_default"
	decoded, err := decodeRunnerContextUsage(runtimekv.Entry{Value: legacy})
	if err != nil || decoded.LimitTokens != 0 || decoded.LimitSource != "unknown" || decoded.UsedTokens != 52733 || decoded.OutputTokens != 969 || decoded.Model != "routed-alias" {
		t.Fatalf("legacy normalization changed receipt: %+v %v", decoded, err)
	}
	if legacy.LimitTokens != 1000000 || legacy.LimitSource != "runner_default" {
		t.Fatal("historical receipt mutated")
	}
}
