package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestContextInspectorSemanticCallsUseDynamicRecorderIdentity(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	provider := &contextUsageRoundModel{}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "fixture"}}}
	model := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: provider, model: "semantic-model"}, contextUsage: recorder}
	if _, err := model.Complete(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	streamModel := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: provider, model: "semantic-model"}, contextUsage: recorder}
	if _, err := streamModel.CompleteStream(t.Context(), request, nil); err != nil {
		t.Fatal(err)
	}
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	calls := inspection["calls"].([]any)
	if len(calls) != 2 || calls[0].(map[string]any)["id"] == calls[1].(map[string]any)["id"] {
		t.Fatalf("semantic invocations not independently recorded: %+v", inspection)
	}
	for _, raw := range calls {
		call := raw.(map[string]any)
		if call["inputMessageId"] != "save-user-message" || call["runnerAttempt"] != float64(f.claim.Attempt) || call["state"] != "complete" {
			t.Fatalf("semantic call lost exact input/claim: %+v", call)
		}
	}
	if inspection["turns"].([]any)[0].(map[string]any)["status"] != "running" {
		t.Fatal("semantic model success invented logical prompt completion")
	}
}

func TestContextInspectorPressurePreparationIsNotSemanticCall(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	provider := &contextUsageRoundModel{}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "assistant", Content: strings.Repeat("a", (800-7)*4)}}}
	for _, streaming := range []bool{false, true} {
		model := &sessionRunnerDynamicModelClient{initialReady: true, initial: sessionRunnerResolvedModelClient{client: provider, model: "fixture-model"}, contextUsage: recorder,
			contextBudget: newRequestContextBudgetFixture(nil, 800, true)}
		var err error
		if streaming {
			_, err = model.CompleteStream(t.Context(), request, nil)
		} else {
			_, err = model.Complete(t.Context(), request)
		}
		var pressure *sessionRunnerRequestContextPressureError
		if !errors.As(err, &pressure) {
			t.Fatalf("controlled pressure missing: %v", err)
		}
	}
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	coverage := inspection["coverage"].(map[string]any)
	if len(provider.requests) != 0 || len(inspection["calls"].([]any)) != 0 || coverage["observedSampleCount"] != float64(2) || coverage["observedCallCount"] != float64(0) {
		t.Fatalf("pre-dispatch preparation became a semantic call: %+v", inspection)
	}
	latest := readContextUsageTest(t, f.server.runtimeStore, f.stream.SessionID)
	if latest.Progress == nil || latest.Progress.Phase != "compacting" {
		t.Fatal("excluding preparation from calls removed the existing live ring progress")
	}
}

func TestContextInspectorControlResumePreservesEarlierCallAssociation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	first := recorder.begin("before-control", agentruntime.ModelRequest{})
	recorder.finish(first, agentruntime.ModelResponse{}, nil)
	checkpoint, _, _, err := f.repo.PauseRunnerForInput(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
		ClientMessageID: "inspect-control-question", Phase: transcriptstore.RunnerPhaseWaitingUser, Resumable: true,
		PayloadJSON: []byte(`{"status":"awaiting_user_response"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), transcriptstore.AppendFrameInputResponseInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, FrameID: f.stream.FrameID,
			ClientMessageID: "inspect-control-answer", PayloadJSON: []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"inspect-control-question","content":"yes"}]}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		RunnerID: "inspect-control-resumed", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
	if err != nil || !resumed.Claimed || resumed.Claim.Attempt != f.claim.Attempt {
		t.Fatalf("in-place resume missing: %+v %v", resumed, err)
	}
	run.Transcript.Claim = resumed.Claim
	after := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	second := after.begin("after-control", agentruntime.ModelRequest{})
	after.finish(second, agentruntime.ModelResponse{}, nil)
	inspection := readContextInspectorFixture(t, f.server, f.stream.SessionID, f.stream.OwnerID)
	if len(inspection["calls"].([]any)) != 2 || len(inspection["turns"].([]any)) != 1 || len(inspection["turns"].([]any)[0].(map[string]any)["callIds"].([]any)) != 2 {
		t.Fatalf("same-prompt resume lost earlier immutable call: %+v", inspection)
	}
	if first.Association.ClaimedInputRevision >= second.Association.ClaimedInputRevision {
		t.Fatal("fixture did not exercise a real input-bound rotation")
	}
}

func TestContextInspectorRejectsForgedAssociationAndAnotherScope(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	sample := recorder.begin("fixture-model", agentruntime.ModelRequest{})
	if sample == nil || sample.Association == nil {
		t.Fatal("missing captured association")
	}
	authority, err := f.repo.ReadContextInspection(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		change func(*runnerContextUsage)
	}{
		{"input-event", func(value *runnerContextUsage) { value.Association.InputEventID++ }},
		{"input-revision", func(value *runnerContextUsage) { value.Association.InputRevision++ }},
		{"claimed-revision", func(value *runnerContextUsage) { value.Association.ClaimedInputRevision++ }},
		{"message-id", func(value *runnerContextUsage) { value.Association.InputMessageID = "another-message" }},
		{"incarnation", func(value *runnerContextUsage) { value.Association.Scope.FrameIncarnationID = "another-incarnation" }},
		{"epoch", func(value *runnerContextUsage) { value.Association.Scope.StreamEpoch++ }},
		{"execution-branch", func(value *runnerContextUsage) { value.Association.Scope.BranchID = "br_unknown" }},
		{"admission-branch", func(value *runnerContextUsage) { value.Association.AdmissionBranchID = "br_unknown" }},
		{"attempt", func(value *runnerContextUsage) { value.Association.RunnerAttempt++ }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			copy := *sample
			association := *sample.Association
			copy.Association = &association
			change.change(&copy)
			inspection := projectContextInspection(authority, runnerContextHistory{TotalObserved: 1, Samples: []runnerContextUsage{copy}})
			if len(inspection.Calls) != 0 || inspection.Coverage.Association == "complete" {
				t.Fatalf("forged/mismatched association joined real prompt: %+v", inspection)
			}
		})
	}
	raw, _ := json.Marshal(sample)
	for _, malformed := range []any{nil, map[string]any{}, map[string]any{"version": true}, map[string]any{"version": 1, "inputRevision": nil}} {
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		value["association"] = malformed
		if _, err := decodeRunnerContextUsage(runtimekv.Entry{Value: value}); err == nil {
			t.Fatalf("malformed optional association accepted: %+v", malformed)
		}
	}
	legacy := *sample
	legacy.ObservationKind = ""
	unknown := projectContextInspection(authority, runnerContextHistory{TotalObserved: 1, Samples: []runnerContextUsage{legacy}})
	if unknown.Coverage.ObservedCallCount != nil || unknown.Coverage.ObservedSampleCount != 1 || len(unknown.Calls) != 0 {
		t.Fatalf("old untyped observation backfilled a model call: %+v", unknown)
	}
	truncated := projectContextInspection(authority, runnerContextHistory{TotalObserved: 129, Samples: []runnerContextUsage{*sample}})
	if truncated.Coverage.ObservedCallCount != nil || !truncated.Coverage.Truncated || truncated.Coverage.Association != "partial" {
		t.Fatalf("bounded history invented a complete call count: %+v", truncated)
	}
	if err := f.repo.ValidateContextInspection(context.Background(), f.stream.OwnerID, authority.Scope); err != nil {
		t.Fatal(err)
	}
}
