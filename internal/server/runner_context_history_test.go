package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	"synon-go/internal/persistence/runtimekv"
)

func TestContextHistoryBoundedRetentionPreservesProviderPeakAndScope(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "history-session", attempt: 1, limit: 10000, limitSource: "model_profile"}
	var firstID string
	for index := 0; index < contextHistoryRetention+3; index++ {
		snapshot := recorder.begin("model", agentruntime.ModelRequest{})
		used := 100 + index
		if index == 0 {
			used, firstID = 9000, snapshot.RequestID
		}
		recorder.finish(snapshot, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: used, TotalTokens: used}}, nil)
	}
	latest, history, found, err := readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || !found || len(history.Samples) != contextHistoryRetention || history.TotalObserved != contextHistoryRetention+3 ||
		history.Peak == nil || history.Peak.RequestID != firstID || history.Peak.UsedTokens != 9000 || history.Peak.LimitTokens != 10000 ||
		history.Samples[len(history.Samples)-1].RequestID != latest.RequestID {
		t.Fatalf("bounded history lost peak/identity: found=%v err=%v history=%+v", found, err, history)
	}
	if _, err := store.DeleteScope(context.Background(), runtimekv.Scope{FrameID: recorder.sessionID}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List(contextUsageNamespace(recorder.sessionID))
	if err != nil || len(entries) != 0 {
		t.Fatalf("task deletion retained history: entries=%d err=%v", len(entries), err)
	}
}

func TestContextHistoryLiveProjectionAndLateCompletionKeepRequestIdentity(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "history-session", attempt: 1, limit: 100, limitSource: "configured"}
	first := recorder.begin("first-model", agentruntime.ModelRequest{})
	recorder.finish(first, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: 80, TotalTokens: 80}}, nil)
	second := recorder.begin("second-model", agentruntime.ModelRequest{}, runnerContextCapacity{Source: "unknown"})
	second.Progress = &runnerContextProgress{Phase: "generating", ObservedAt: time.Now().UTC(), UsedTokens: second.UsedTokens + 5, OutputTokens: 5}
	if err := recorder.persist(*second, true); err != nil {
		t.Fatal(err)
	}
	latest, history, _, err := readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || latest.Progress == nil || history.Samples[1].Progress == nil || history.Samples[1].Progress.OutputTokens != 5 || history.Samples[1].LimitTokens != 0 {
		t.Fatalf("live projection stale or rebound old capacity: %v %+v", err, history)
	}
	recorder.finish(first, agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{InputTokens: 999, TotalTokens: 999}}, nil)
	latest, history, _, err = readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || latest.RequestID != second.RequestID || history.TotalObserved != 2 || history.Peak.UsedTokens != 80 {
		t.Fatalf("late completion replaced a newer request: %v %+v", err, history)
	}
	second.Progress = &runnerContextProgress{Phase: "compacting", ObservedAt: time.Now().UTC(), UsedTokens: second.UsedTokens}
	if err := recorder.persist(*second, true); err != nil {
		t.Fatal(err)
	}
	third := recorder.begin("second-model", agentruntime.ModelRequest{}, runnerContextCapacity{Source: "unknown"})
	_, history, _, err = readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || history.Samples[1].Progress == nil || history.Samples[1].Progress.Phase != "compacting" || history.Samples[2].RequestID != third.RequestID {
		t.Fatalf("observed compaction pressure lost: %v %+v", err, history)
	}
}

func TestContextHistoryLegacyCoverageAndInvalidHistoryAreExplicit(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "history.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "history-session", limit: 100, limitSource: "configured"}
	snapshot := recorder.begin("model", agentruntime.ModelRequest{})
	if _, err := store.Delete(contextUsageNamespace(recorder.sessionID), "history"); err != nil {
		t.Fatal(err)
	}
	_, history, _, err := readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || history.Coverage != "latest_only" || len(history.Samples) != 1 || history.TotalObserved != 1 {
		t.Fatalf("invented historical coverage: %v %+v", err, history)
	}
	if _, err := store.Set(contextUsageNamespace(recorder.sessionID), "history", runnerContextHistory{
		SessionID: "foreign", Coverage: "recorded", TotalObserved: 1, Samples: []runnerContextUsage{*snapshot},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readRunnerContextUsage(store, recorder.sessionID); err == nil {
		t.Fatal("foreign/corrupt history silently presented as valid")
	}
}
