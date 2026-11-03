package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestContextInspectorPinsRuntimeRequestSetBeforeJournalRead(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	recorder := f.server.newClaimedContextUsageRecorder(t.Context(), f.stream.SessionID, run, SessionRunnerChatOptions{})
	sample := recorder.begin("fixture-model", agentruntime.ModelRequest{})
	if sample == nil || sample.Association == nil {
		t.Fatal("missing captured association")
	}
	history := runnerContextHistory{SessionID: f.stream.SessionID, TotalObserved: contextHistoryRetention, Coverage: "recorded"}
	for index := 0; index < contextHistoryRetention; index++ {
		value := *sample
		value.RequestID, value.State = fmt.Sprintf("retained-request-%03d", index), "complete"
		history.Samples = append(history.Samples, value)
	}
	if err := f.server.runtimeStore.EditNamespace(contextUsageNamespace(f.stream.SessionID), func(entries map[string]runtimekv.Entry) (bool, error) {
		entries["history"] = runtimekv.Entry{Value: history}
		entries["latest"] = runtimekv.Entry{Value: history.Samples[len(history.Samples)-1]}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	var later *runnerContextUsage
	bundle, err := readContextUsageInspectionBundle(f.stream.SessionID, func() (runnerContextUsage, runnerContextHistory, bool, error) {
		order = append(order, "runtime")
		return readRunnerContextUsage(f.server.runtimeStore, f.stream.SessionID)
	}, func() (*transcriptstore.ContextInspectionSnapshot, string, error) {
		order = append(order, "journal")
		// Real recorder retention advances between the two readers; it must
		// never replace the owned request-ID set of this inspection response.
		later = recorder.begin("next-request", agentruntime.ModelRequest{})
		if _, _, _, err := f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
			ClientMessageID: "inspection-between-readers", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true,
			PayloadJSON: []byte(`{"status":"running"}`)}); err != nil {
			return nil, "", err
		}
		value, err := f.repo.ReadContextInspection(t.Context(), f.stream.UID, f.stream.OwnerID)
		return &value, f.stream.OwnerID, err
	})
	if err != nil || bundle.inspectionError != nil || bundle.authority == nil || later == nil || strings.Join(order, ",") != "runtime,journal" {
		t.Fatalf("ordered read failed: order=%v err=%v authorityErr=%v", order, err, bundle.inspectionError)
	}
	latest, live, found, err := readRunnerContextUsage(f.server.runtimeStore, f.stream.SessionID)
	if err != nil || !found || latest.RequestID != later.RequestID || live.TotalObserved != contextHistoryRetention+1 ||
		live.Samples[0].RequestID != history.Samples[1].RequestID {
		t.Fatalf("fixture did not roll over: found=%v err=%v", found, err)
	}
	if bundle.snapshot.RequestID != history.Samples[contextHistoryRetention-1].RequestID || bundle.history.TotalObserved != contextHistoryRetention ||
		bundle.history.Samples[0].RequestID != history.Samples[0].RequestID {
		t.Fatal("new sample leaked into fixed runtime read")
	}
	projection := projectContextInspection(*bundle.authority, bundle.history)
	if len(projection.Calls) != contextHistoryRetention || projection.Coverage.UnassociatedCallCount != 0 {
		t.Fatalf("fixed exact joins lost: %+v", projection.Coverage)
	}
	for _, call := range projection.Calls {
		if call.ID == later.RequestID {
			t.Fatal("later semantic request manufactured in as-of call list")
		}
	}
	if err := f.repo.ValidateContextInspection(t.Context(), bundle.ownerID, bundle.authority.Scope); err != nil {
		t.Fatal(err)
	}
	missing := projectContextInspection(*bundle.authority, runnerContextHistory{TotalObserved: contextHistoryRetention + 1})
	if len(missing.Calls) != 0 || missing.Coverage.ObservedCallCount != nil || !missing.Coverage.Truncated {
		t.Fatalf("trimmed usage invented calls or a known count: %+v", missing.Coverage)
	}
}

func TestContextInspectorErrorCodesKeepIdentityAndSnapshotConflictsDistinct(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"scope", transcriptstore.ErrBranchStateStale, http.StatusConflict, "context_scope_changed"},
		{"owner", transcriptstore.ErrOwnerMismatch, http.StatusConflict, "context_scope_changed"},
		{"snapshot", transcriptstore.ErrEventConflict, http.StatusConflict, "context_snapshot_conflict"},
		{"storage", errors.New("synthetic unavailable"), http.StatusServiceUnavailable, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeContextInspectionError(response, c.err)
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			code, _ := body["code"].(string)
			if response.Code != c.status || code != c.code {
				t.Fatalf("wrong error contract: status=%d body=%v", response.Code, body)
			}
		})
	}
}
