package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

// Exercise the actual observer -> dynamic recorder -> durable association ->
// existing inspect projection, rather than asserting a newly introduced type.
func TestCompletedCallPhaseTimingWiring(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	calls := 0
	provider := communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		calls++
		for _, event := range []agentruntime.ModelStreamEvent{
			{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true},
			{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "fixture native candidate"},
			{Kind: agentruntime.ModelStreamEventToolCallBoundary},
		} {
			if err := emit(event); err != nil {
				return agentruntime.ModelResponse{}, err
			}
		}
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "fixture native candidate"}}, nil
	}}
	dynamic := &sessionRunnerDynamicModelClient{initialReady: true,
		initial: sessionRunnerResolvedModelClient{client: provider, model: "phase-fixture"}, contextUsage: recorder}
	observer := &sessionRunnerCommunicationObserver{delegate: dynamic}
	if _, err := observer.CompleteStream(t.Context(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.noteProgressReady("native")
	observer.notePublication() // Existing hook is called only after durable publication succeeds.
	observer.recordBoundary(false)
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	items := inspection["calls"].([]any)
	if calls != 1 || len(items) != 1 {
		t.Fatalf("observation changed actual calls: delegate=%d inspection=%+v", calls, inspection)
	}
	call := items[0].(map[string]any)
	if call["inputMessageId"] != "save-user-message" || call["state"] != "complete" {
		t.Fatalf("lost existing completed-call association: %+v", call)
	}
	phases, ok := call["phaseTiming"].(map[string]any)
	if !ok {
		t.Fatalf("completed call has no recorded phase timing: %+v", call)
	}
	if phases["origin"] != "observer_entry" || phases["version"] != float64(1) {
		t.Fatalf("unknown timing origin: %+v", phases)
	}
	offsets := phases["offsetsUs"].(map[string]any)
	for _, key := range []string{"firstPrivateActivity", "firstNativeContent", "firstToolBoundary", "firstPublicProgressReady", "firstDurablePublication", "delegateFinished"} {
		if value, ok := offsets[key].(float64); !ok || value < 0 {
			t.Errorf("missing/nonnegative phase %s: %+v", key, phases)
		}
	}
	if spans, ok := phases["preparation"].([]any); !ok || len(spans) != 3 {
		t.Fatalf("resolve/prepare/budget spans were not recorded: %+v", phases)
	}
	if _, invented := phases["transportAttempts"]; invented {
		t.Fatal("fake delegate invented HTTP evidence")
	}
}

func TestCompletedCallPhaseTimingAssociation(t *testing.T) {
	t.Run("late completion cannot enrich a newer request or historical row", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
		recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
		first := recorder.begin(t.Context(), "first", agentruntime.ModelRequest{})
		phase := newRunnerCallPhaseScope(nil).current
		phase.bind(recorder, first)
		phase.note("firstNativeContent")
		recorder.finish(first, agentruntime.ModelResponse{}, nil)
		second := recorder.begin(t.Context(), "second", agentruntime.ModelRequest{})
		recorder.finish(second, agentruntime.ModelResponse{}, nil)
		phase.freeze()
		phase.note("firstPrivateActivity")
		inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
		for _, raw := range inspection["calls"].([]any) {
			if _, present := raw.(map[string]any)["phaseTiming"]; present {
				t.Fatal("late metadata rewrote a rotated call")
			}
		}
	})
	t.Run("auxiliary calls do not acquire main-call metadata", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
		recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
		dynamic := &sessionRunnerDynamicModelClient{initialReady: true,
			initial: sessionRunnerResolvedModelClient{client: &contextUsageRoundModel{}, model: "auxiliary"}, contextUsage: recorder}
		observer := &sessionRunnerCommunicationObserver{delegate: dynamic}
		if _, err := observer.CompleteStream(withAuxiliaryContextUsage(t.Context()), agentruntime.ModelRequest{}, nil); err != nil {
			t.Fatal(err)
		}
		inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
		if len(inspection["calls"].([]any)) != 0 {
			t.Fatal("auxiliary call became a main-model call")
		}
	})
}

func TestCompletedCallPhaseTimingCompatibility(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	snapshot := recorder.begin(t.Context(), "compatibility", agentruntime.ModelRequest{})
	recorder.finish(snapshot, agentruntime.ModelResponse{}, nil)
	raw, _ := json.Marshal(snapshot)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []any{"damaged", 7, []any{}, map[string]any{"version": 2},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{"firstNativeContent": -1}},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{"delegateFinished": "not a number"}},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{"firstNativeContent": nil}, "truncated": false, "saturated": false},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{"firstNativeContent": 1.5}, "truncated": false, "saturated": false},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{"firstNativeContent": contextHistorySafeInteger + 1}, "truncated": false, "saturated": false},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{}},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": nil, "truncated": false, "saturated": false},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{}, "truncated": nil, "saturated": false},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{}, "truncated": false, "saturated": false,
			"preparation": []any{map[string]any{"stage": "resolve", "endUs": 1}}},
		map[string]any{"version": 1, "origin": "observer_entry", "offsetsUs": map[string]any{}, "truncated": false, "saturated": false,
			"transportAttempts": []any{map[string]any{"attempt": 1, "protocol": providers.ProtocolOpenAICompatible, "offsetsUs": map[string]any{"attemptEntry": nil}}}}} {
		fields["phaseTiming"] = invalid
		got, err := decodeRunnerContextUsage(runtimekv.Entry{Value: fields})
		if err != nil || got.PhaseTiming != nil || got.RequestID != snapshot.RequestID || got.State != "complete" || got.UsedTokens != snapshot.UsedTokens {
			t.Errorf("damaged optional timing was accepted or removed valid call/usage: invalid=%#v got=%+v err=%v", invalid, got, err)
		}
	}
	start := time.Now()
	phase := newRunnerCallPhaseScope(func() time.Time { return start }).current
	phase.note("firstPrivateActivity")
	saturated := true
	for i := 0; i < maxCallPhaseEntries+1; i++ {
		phase.transport(providers.CompletedTransportAttempt{Attempt: i + 1, Protocol: providers.ProtocolOpenAICompatible, StartedAt: start,
			Diagnostics: &providers.ProviderTransportDiagnostics{Version: 1, CountsSaturated: &saturated}})
	}
	phase.freeze()
	phase.note("firstNativeContent") // A late producer cannot mutate frozen evidence.
	if phase.value.OffsetsUs["firstPrivateActivity"] != 0 || len(phase.value.TransportAttempts) != maxCallPhaseEntries || !phase.value.Truncated || !phase.value.Saturated {
		t.Fatal("zero, bounded retention or explicit truncation was lost")
	}
	if _, exists := phase.value.OffsetsUs["firstNativeContent"]; exists {
		t.Fatal("late callback mutated frozen evidence")
	}
	// Use the actual failed-call shape: changing only a settled complete call's
	// state would retain nonzero output counters that the existing reader rejects.
	failed := recorder.begin(t.Context(), "cancelled", agentruntime.ModelRequest{})
	recorder.finish(failed, agentruntime.ModelResponse{}, context.Canceled)
	raw, _ = json.Marshal(failed)
	fields = nil
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["phaseTiming"] = &phase.value
	got, err := decodeRunnerContextUsage(runtimekv.Entry{Value: fields})
	if err != nil || got.RequestID != failed.RequestID || got.State != "failed" || got.FailureKind != "cancelled" || got.OutputTokens != 0 ||
		got.PhaseTiming == nil || !got.PhaseTiming.Saturated || !got.PhaseTiming.Truncated {
		t.Fatalf("failed completed call lost bounded phase evidence: %+v %v", got, err)
	}
	if zero, present := got.PhaseTiming.OffsetsUs["firstPrivateActivity"]; !present || zero != 0 {
		t.Fatal("the reader lost a recorded zero offset")
	}
	if _, invented := got.PhaseTiming.TransportAttempts[0].OffsetsUs["firstDecodedGrowth"]; invented {
		t.Fatal("the reader invented zero for unobserved decoded growth")
	}
}
