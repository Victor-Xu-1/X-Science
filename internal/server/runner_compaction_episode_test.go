package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func compactionEpisodeModelFixture() runnerCompactionModel {
	return runnerCompactionModel{
		selectionKind: "provider", selectionRevision: 0, providerID: "provider-a", modelID: "model-a",
		capacity: runnerContextCapacity{Tokens: 2000, Source: "model_profile"},
	}
}

func compactionEpisodeReplayFixture(binding runnerCompactionBinding) []eventjournal.Entry {
	return []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "inspect input"}},
		{EventID: 2, Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolPhase": "auto_compact", "summary": "durable handoff",
			runnerSoftCompactionEpisodeField: binding,
		}},
		{EventID: 3, Message: eventjournal.Message{"type": "runner_checkpoint", "stage": "preparing"}},
	}
}

func TestRunnerCompactionEpisodeBindingRoundTripPreservesIdentity(t *testing.T) {
	model := compactionEpisodeModelFixture()
	// The existing provider contract has no per-ID cap. Distinct long IDs must
	// remain distinct, and revision zero must survive strict presence checks.
	model.providerID = strings.Repeat("public-provider-", 100)
	model.modelID = strings.Repeat("public-model-", 100)
	want := model.binding(100)
	value, err := want.checkpointValue()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{runnerSoftCompactionEpisodeField: value})
	if err != nil {
		t.Fatal(err)
	}
	var message eventjournal.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if got, bound := runnerCompactionBindingFromMessage(message); !bound || got != want {
		t.Fatalf("binding=%+v bound=%t want=%+v", got, bound, want)
	}
	model.providerID += "different"
	if model.binding(100) == want {
		t.Fatal("distinct public IDs collided")
	}
	model.providerID = ""
	unknown := model.binding(100)
	if unknown.ProviderIDKnown || unknown.ProviderID != "" {
		t.Fatal("fallback invented a provider identity")
	}
	model.modelID = string([]byte{0xff})
	if _, err := model.binding(100).checkpointValue(); err == nil {
		t.Fatal("invalid UTF-8 would silently change persisted identity")
	}
	for selection, wantKind := range map[string]string{
		"": "default", "provider:opaque-private-selection": "provider", "opaque-private-selection": "model",
	} {
		if got := runnerCompactionSelectionKind(selection); got != wantKind {
			t.Fatalf("selection kind=%q want=%q", got, wantKind)
		}
	}
}

func TestRunnerCompactionEpisodeReplayDistinguishesProgressAndLegacy(t *testing.T) {
	binding := compactionEpisodeModelFixture().binding(100)
	for _, test := range []struct {
		name    string
		message eventjournal.Message
		armed   bool
	}{
		{"preparation", eventjournal.Message{"type": "runner_checkpoint", "stage": "preparing"}, false},
		{"proposal", eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": "proposal"}}}, false},
		{"user", eventjournal.Message{"type": "message", "role": "user", "text": "new input"}, true},
		{"assistant", eventjournal.Message{"type": "message", "role": "assistant", "text": "new answer"}, true},
		{"tool_completed", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": "completed"}, true},
		{"tool_failed", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": "failed"}, true},
		{"legacy_unknown", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "legacy", "toolPhase": "completed"}, false},
		{"tool_start", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": "start"}, false},
		{"prestart", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": prestartToolFailurePhase}, false},
		{"rejected", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": "completed", "rejectedBeforeExecution": true}, false},
		{"not_executed", eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "call", "toolPhase": "completed", "toolResult": map[string]any{"executed": false}}, false},
		{"provider_pressure", eventjournal.Message{"type": "runner_checkpoint", "reason_code": sessionRunnerProviderContextPressureReasonCode}, true},
		{"request_pressure", eventjournal.Message{"type": "runner_checkpoint", "reason_code": sessionRunnerRequestContextPressureReasonCode}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := eventjournal.Entry{EventID: 5, Message: test.message}
			if stringValue(test.message["toolCallId"]) == "call" {
				candidate.RuntimeProjection = transcriptstore.RunnerReplayToolOutcome{
					BatchID: "canonical-batch", SourceEventID: 4, Ordinal: 0, ToolCallID: "call", TerminalEventID: 5,
				}
			}
			entries := append(compactionEpisodeReplayFixture(binding), candidate)
			if got := restoreRunnerCompactionEpisode(binding, entries).armed; got != test.armed {
				t.Fatalf("armed=%t want=%t", got, test.armed)
			}
		})
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []string{"missing", "unknown_version", "missing_revision", "null_revision", "extra_field"} {
		t.Run(test, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			switch test {
			case "unknown_version":
				value["version"] = 2
			case "missing_revision":
				delete(value, "selectionRevision")
			case "null_revision":
				value["selectionRevision"] = nil
			case "extra_field":
				value["unrecognized"] = true
			}
			entries := compactionEpisodeReplayFixture(binding)
			entries[1].Message[runnerSoftCompactionEpisodeField] = value
			if test == "missing" {
				delete(entries[1].Message, runnerSoftCompactionEpisodeField)
			}
			before := mustMarshalRawMessage(entries[1].Message)
			if !restoreRunnerCompactionEpisode(binding, entries).armed || !reflect.DeepEqual(mustMarshalRawMessage(entries[1].Message), before) {
				t.Fatal("unbound legacy compact borrowed current identity or was rewritten")
			}
		})
	}
}

func TestRunnerCompactionEpisodeRequiresCanonicalTerminalProjection(t *testing.T) {
	binding := compactionEpisodeModelFixture().binding(100)
	compact := compactionEpisodeReplayFixture(binding)[1]
	compact.EventID = 10
	original := transcriptstore.RunnerReplayToolOutcome{
		BatchID: "canonical-only-batch", SourceEventID: 4, Ordinal: 0, ToolCallID: "coordinate-call", TerminalEventID: 15,
	}
	for _, test := range []struct {
		name      string
		change    func(*transcriptstore.RunnerReplayToolOutcome)
		newResult bool
	}{
		{"canonical", func(*transcriptstore.RunnerReplayToolOutcome) {}, true},
		{"payload_only", nil, false},
		{"republished_old_terminal", func(value *transcriptstore.RunnerReplayToolOutcome) { value.TerminalEventID = 5 }, false},
		{"wrong_call", func(value *transcriptstore.RunnerReplayToolOutcome) { value.ToolCallID = "other-call" }, false},
		{"missing_batch", func(value *transcriptstore.RunnerReplayToolOutcome) { value.BatchID = "" }, false},
		{"missing_source", func(value *transcriptstore.RunnerReplayToolOutcome) { value.SourceEventID = 0 }, false},
		{"reversed_source", func(value *transcriptstore.RunnerReplayToolOutcome) { value.SourceEventID = 16 }, false},
		{"invalid_ordinal", func(value *transcriptstore.RunnerReplayToolOutcome) { value.Ordinal = -1 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := eventjournal.Entry{EventID: 15, Message: eventjournal.Message{
				"type": "runner_checkpoint", "toolCallId": "coordinate-call", "toolPhase": "completed",
				// Payload lookalikes are ordinary untrusted history, not the
				// SQL-only RuntimeProjection consumed by the episode.
				"ToolOutcome": original, "RuntimeProjection": original, "toolOriginAttempt": float64(1),
			}}
			if test.change != nil {
				outcome := original
				test.change(&outcome)
				candidate.RuntimeProjection = outcome
			}
			if got := restoreRunnerCompactionEpisode(binding, []eventjournal.Entry{compact, candidate}).armed; got != test.newResult {
				t.Fatalf("canonical outcome new=%t want=%t", got, test.newResult)
			}
		})
	}
	encoded, err := json.Marshal(eventjournal.Entry{
		EventID: 15, Message: eventjournal.Message{"type": "runner_checkpoint", "toolCallId": "coordinate-call", "toolPhase": "completed"},
		RuntimeProjection: original,
	})
	if err != nil || strings.Contains(string(encoded), original.BatchID) || strings.Contains(string(encoded), "RuntimeProjection") ||
		strings.Contains(string(encoded), "SourceEventID") || strings.Contains(string(encoded), "TerminalEventID") {
		t.Fatalf("runtime-only provenance leaked into journal JSON: %s err=%v", encoded, err)
	}
}

func TestRunnerRequestBudgetCompactionEpisodeRearmsOnBindingChange(t *testing.T) {
	_, large := requestBudgetFixture()
	base := compactionEpisodeModelFixture()
	for _, test := range []struct {
		name   string
		change func(*runnerCompactionModel)
	}{
		{"selection_kind", func(model *runnerCompactionModel) { model.selectionKind = "model" }},
		{"revision", func(model *runnerCompactionModel) { model.selectionRevision++ }},
		{"provider", func(model *runnerCompactionModel) { model.providerID = "provider-b" }},
		{"model", func(model *runnerCompactionModel) { model.modelID = "model-b" }},
		{"window", func(model *runnerCompactionModel) { model.capacity.Tokens++ }},
		{"window_source", func(model *runnerCompactionModel) { model.capacity.Source = "configured" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := &sessionRunnerRequestContextBudget{episode: restoreRunnerCompactionEpisode(base.binding(100), compactionEpisodeReplayFixture(base.binding(100)))}
			if err := budget.beforeCall(context.Background(), large, base); err != nil {
				t.Fatalf("unchanged compact retriggered: %v", err)
			}
			next := base
			test.change(&next)
			var pressure *sessionRunnerRequestContextPressureError
			if err := budget.beforeCall(context.Background(), large, next); !errors.As(err, &pressure) {
				t.Fatalf("changed binding did not rearm: %v", err)
			}
			if err := budget.beforeCall(context.Background(), large, next); err != nil {
				t.Fatalf("unchanged next request looped: %v", err)
			}
		})
	}
}

func TestRunnerRequestBudgetCompactionEpisodeRearmsOnThresholdChange(t *testing.T) {
	server := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, server) })
	model := compactionEpisodeModelFixture()
	budget := &sessionRunnerRequestContextBudget{server: server,
		episode: restoreRunnerCompactionEpisode(model.binding(100), compactionEpisodeReplayFixture(model.binding(100)))}
	_, large := requestBudgetFixture()
	for index, threshold := range []int{100, 200} {
		if _, err := server.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), float64(threshold)); err != nil {
			t.Fatal(err)
		}
		err := budget.beforeCall(t.Context(), large, model)
		if index == 0 && err != nil {
			t.Fatalf("same policy retriggered: %v", err)
		}
		var pressure *sessionRunnerRequestContextPressureError
		if index == 1 && (!errors.As(err, &pressure) || pressure.threshold != threshold) {
			t.Fatalf("changed threshold did not rearm: %v", err)
		}
	}
}

func TestRunnerCompactionEpisodeBudgetSkipsAuxiliaryCancelledAndDisabledTransitions(t *testing.T) {
	_, large := requestBudgetFixture()
	base := compactionEpisodeModelFixture()
	next := base
	next.selectionRevision++
	server := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, server) })
	for _, mode := range []string{"auxiliary", "cancelled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			episode := restoreRunnerCompactionEpisode(base.binding(100), compactionEpisodeReplayFixture(base.binding(100)))
			budget := &sessionRunnerRequestContextBudget{episode: episode, server: server}
			ctx := context.Background()
			if mode == "auxiliary" {
				ctx = withAuxiliaryContextUsage(ctx)
			}
			if mode == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if mode == "disabled" {
				if _, err := server.settingsStore.Set(configStoreKey("autoCompactEnabled"), false); err != nil {
					t.Fatal(err)
				}
			}
			if err := budget.beforeCall(ctx, large, next); mode == "cancelled" && !errors.Is(err, context.Canceled) || mode != "cancelled" && err != nil {
				t.Fatalf("%s admission: %v", mode, err)
			}
			budget.noteDurableProgress(ctx)
			if episode.armed || episode.binding != base.binding(100) {
				t.Fatal("excluded call changed the episode")
			}
		})
	}
}
