package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func sourceObservationReceipt(callID, outer string) eventjournal.Message {
	input, result := map[string]any{"query": "a controlled query", "page": 1}, map[string]any{"total_count": 0, "returned_count": 0, "has_more": false}
	inputRaw, _ := json.Marshal(input)
	resultRaw, _ := json.Marshal(result)
	return eventjournal.Message{
		"type": "runner_checkpoint", "schema": "x-science.kernel_mcp_evidence.v1", "status": "completed", "toolPhase": "completed",
		"toolName": "mcp__fixture__search", "toolCallId": callID, "hostCallId": callID, "outerToolCallId": outer,
		"kernelOperationId": "operation-" + outer, "executionId": "execution-" + outer, "kernelId": "kernel", "kernelGeneration": 1,
		"evidenceClass": "bundled-readonly-source-v1", "connectorId": "bundled:fixture", "connectorSource": "bundled", "readOnlyHint": true,
		"inputSchemaSha256": strings.Repeat("a", 64), "requestSha256": kernelMCPEvidenceSHA256(inputRaw), "resultSha256": kernelMCPEvidenceSHA256(resultRaw),
		"toolInput": input, "toolResult": result,
	}
}

func sourceObservationOuter(callID, label string, extra map[string]any) eventjournal.Message {
	result := map[string]any{"ok": true, "stdout": label, "files_written": []any{}, "dropped_roots": []any{}}
	for key, value := range extra {
		result[key] = value
	}
	return eventjournal.Message{"type": "runner_checkpoint", "status": "completed", "toolPhase": "completed", "toolName": "repl", "toolCallId": callID, "toolResult": result}
}

func TestRunnerSourceObservationLiveAndReplayIgnoreOnlyPresentation(t *testing.T) {
	var live, replay runnerSourceObservationReducer
	for i, label := range []string{"pmids:", "pmids:", "pmIds:", "pmids:"} {
		callID := fmt.Sprint("outer-", i)
		source, outer := sourceObservationReceipt(fmt.Sprint("host-", i), callID), sourceObservationOuter(callID, label, nil)
		live.observe(source)
		got := live.completeLive(agentruntime.ToolCall{ID: callID, Name: "repl"}, outer["toolResult"])
		committed, _ := live.observe(outer)
		replay.observe(source)
		restored, _ := replay.observe(outer)
		if got.State != restored.State || got.RepeatedOperations != restored.RepeatedOperations || *got != *committed {
			t.Fatalf("live/replay drift %d: %+v %+v %+v", i, got, committed, restored)
		}
		if got.RepeatedOperations != i {
			t.Fatalf("cosmetic wrapper reset source window: %+v", got)
		}
	}
	bad := sourceObservationReceipt("bad", "bad-outer")
	bad["resultSha256"] = strings.Repeat("f", 64)
	replay.observe(bad)
	got, _ := replay.observe(sourceObservationOuter("bad-outer", "looks identical", nil))
	if got.State != agentruntime.SourceObservationUnknown {
		t.Fatalf("bad hash became verified source: %+v", got)
	}
}

func TestRunnerSourceObservationMaterialChangeAndFailure(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"file":                      {"files_written": []any{"new.dat"}},
		"artifact":                  {"artifacts": []any{map[string]any{"version_id": "immutable-version"}}},
		"computation without files": {"effect": map[string]any{"schema": agentruntime.ToolEffectSchema, "state": string(agentruntime.ToolEffectChanged), "categories": []any{"computation"}}},
		"actual failure":            {"ok": false},
	} {
		t.Run(name, func(t *testing.T) {
			var reducer runnerSourceObservationReducer
			for _, callID := range []string{"first", "second"} {
				reducer.observe(sourceObservationReceipt("host-"+callID, callID))
				reducer.observe(sourceObservationOuter(callID, "same", nil))
			}
			reducer.observe(sourceObservationReceipt("host-change", "changed"))
			got, _ := reducer.observe(sourceObservationOuter("changed", "same", extra))
			if got.RepeatedOperations != 0 {
				t.Fatalf("real change/failure classified repeated: %+v", got)
			}
		})
	}
}

func TestRunnerSourceObservationUnsuccessfulReceiptsNeverBecomeNew(t *testing.T) {
	for name, result := range map[string]map[string]any{
		"unavailable":  {"sourceUnavailable": true},
		"failed":       {"ok": false},
		"partial":      {"partial": true},
		"not executed": {"ok": true, "executed": false},
	} {
		t.Run(name, func(t *testing.T) {
			var live, replay runnerSourceObservationReducer
			source := sourceObservationReceipt("unsuccessful-host", "unsuccessful-outer")
			source["toolResult"] = result
			raw, _ := json.Marshal(result)
			source["resultSha256"] = kernelMCPEvidenceSHA256(raw)
			if _, verified := runnerVerifiedSourceIdentity(source); !verified {
				t.Fatal("fixture must retain a structurally valid durable source identity")
			}
			live.observe(source)
			replay.observe(source)
			outer := sourceObservationOuter("unsuccessful-outer", "a fresh computed result", nil)
			observed := live.completeLive(agentruntime.ToolCall{ID: "unsuccessful-outer", Name: "repl"}, outer["toolResult"])
			restored, _ := replay.observe(outer)
			if observed.State != agentruntime.SourceObservationUnknown || *observed != *restored || observed.RepeatedOperations != 0 {
				t.Fatalf("unsuccessful source became new evidence: live=%+v replay=%+v", observed, restored)
			}
			if !runnerSourceAwareToolProgress(observed, false, "repl", outer["toolResult"]) {
				t.Fatal("unknown source discarded the outer cell's ordinary computation")
			}
		})
	}
}

func TestRunnerSourceObservationUnknownRetainsOuterOutcomeAuthority(t *testing.T) {
	unknown := &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown, HasSources: true}
	for _, test := range []struct {
		name     string
		result   map[string]any
		progress bool
	}{
		{"fresh independent computation", map[string]any{"ok": true, "stdout": "fresh variable result", "files_written": []any{}}, true},
		{"typed failed outer", map[string]any{"ok": false, "stdout": "diagnostic"}, false},
		{"typed unavailable outer", map[string]any{"sourceUnavailable": true, "stdout": "diagnostic"}, false},
		{"outer did not execute", map[string]any{"ok": true, "executed": false}, false},
		{"repeated execution evidence", map[string]any{"ok": true, "stdout": "same", "recovery_evidence_repeated": true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runnerSourceAwareToolProgress(unknown, false, "repl", test.result); got != test.progress {
				t.Fatalf("ordinary outer progress=%t; want %t", got, test.progress)
			}
		})
	}
}

func TestRunnerSourceObservationRecoveryModeSurvivesReplayWithoutClosingActions(t *testing.T) {
	scope := runnerRecoveryObligationFingerprint(nil, transcriptstore.RunnerInterruptionCause{})
	state := newSessionRunnerNoProgressRecovery(scope)
	entries := []eventjournal.Entry{}
	appendEntry := func(entry eventjournal.Entry) {
		entries = append(entries, entry)
		state.observeEntry(entry, nil)
	}
	appendEntry(eventjournal.Entry{SourceEventType: "user_message", Message: eventjournal.Message{"type": "message", "role": "user"}})
	appendStep := func(proposal int64, callID, label string, extra map[string]any) {
		appendEntry(eventjournal.Entry{EventID: proposal, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": callID}}}})
		source := sourceObservationReceipt("host-"+callID, callID)
		key, _ := runnerVerifiedSourceIdentity(source)
		appendEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: source, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: proposal}})
		outer := sourceObservationOuter(callID, label, extra)
		appendEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: outer, RuntimeProjection: runnerSourceObservationWitness{proposalEventID: proposal, executionResult: outer["toolResult"]}})
	}
	appendStep(10, "first", "pmids:", nil)
	state.recordNoProgress([]agentruntime.ToolCall{{Name: "repl", Arguments: json.RawMessage(`{"code":"unknown_write()"}`)}}, true)
	if len(state.ClosedActions) != 0 {
		t.Fatal("source mode closed arbitrary execution")
	}
	entries = append(entries, eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "status": "interrupted", "reason_code": sessionRunnerToolRoundNoProgressReasonCode, "resume_detail": state.resumeDetail("Verified source content repeated; effects unknown."), "recovery_contract_revision": sessionRunnerRecoveryContractRevision}})
	appendStep(20, "second", "pmIds:", nil)
	restored := runnerNoProgressRecoveryFromEntries(entries, nil)
	if !restored.SourceRepetition || restored.Consecutive < 1 || len(restored.ClosedActions) != 0 {
		t.Fatalf("source mode degraded on restart: %+v", restored)
	}
	context := sessionRunnerNoProgressRecoveryContext(restored, nil)
	if !strings.Contains(context, "does not prove") || strings.Contains(context, "cannot execute again") || strings.Contains(context, "idempotent") {
		t.Fatalf("unsafe resumed feedback: %s", context)
	}
	appendStep(30, "third", "same", map[string]any{"files_written": []any{"new.dat"}})
	if state.Consecutive != 0 || state.SourceRepetition {
		t.Fatalf("actual write did not reopen progress: %+v", state)
	}
}

func TestRunnerSourceObservationInputBoundaryExcludesLateOldOperation(t *testing.T) {
	var reducer runnerSourceObservationReducer
	input := func(id int64) {
		reducer.observeEntry(eventjournal.Entry{EventID: id, SourceEventType: "user_input_response", Message: eventjournal.Message{"role": "user"}})
	}
	admit := func(eventID int64, callID string) {
		reducer.observeEntry(eventjournal.Entry{EventID: eventID, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": callID}}}})
	}
	input(10)
	admit(11, "same")
	input(20) // a staged input was admitted while the old operation was finishing
	admit(21, "same")
	oldSource := sourceObservationReceipt("late-old-host", "same")
	key, _ := runnerVerifiedSourceIdentity(oldSource)
	late, _ := reducer.observeEntry(eventjournal.Entry{Message: oldSource, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: 11}})
	if late != nil || reducer.admitted["same"] != 21 {
		t.Fatalf("old source consumed new proposal admission: %+v", late)
	}
	late, _ = reducer.observeEntry(eventjournal.Entry{Message: sourceObservationOuter("same", "pmids:", nil), RuntimeProjection: runnerSourceObservationWitness{proposalEventID: 11, executionResult: map[string]any{"ok": true}}})
	if late != nil || reducer.admitted["same"] != 21 {
		t.Fatalf("old completion consumed new proposal: %+v", late)
	}
	reducer.observeEntry(eventjournal.Entry{Message: sourceObservationReceipt("new-host", "same"), RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: 21}})
	fresh, _ := reducer.observeEntry(eventjournal.Entry{Message: sourceObservationOuter("same", "pmIds:", nil), RuntimeProjection: runnerSourceObservationWitness{proposalEventID: 21, executionResult: sourceObservationOuter("same", "pmIds:", nil)["toolResult"]}})
	if fresh.State != agentruntime.SourceObservationNew || fresh.RepeatedOperations != 0 {
		t.Fatalf("old source polluted new input: %+v", fresh)
	}
}

func TestRunnerSourceObservationDirectReceiptCompletesExactlyOnce(t *testing.T) {
	var live, replay runnerSourceObservationReducer
	for i := 0; i < 4; i++ {
		callID := fmt.Sprint("direct-", i)
		value := map[string]any{"ok": true, "result": "unchanged"}
		if before := live.completeLive(agentruntime.ToolCall{ID: callID, Name: "mcp__fixture__search"}, value); before.State != agentruntime.SourceObservationUnknown {
			t.Fatalf("uncommitted direct evidence was promoted: %+v", before)
		}
		message := sourceObservationReceipt(callID, callID)
		message["schema"] = workspaceMCPSourceEvidenceSchemaV1
		for _, key := range []string{"outerToolCallId", "kernelOperationId", "executionId", "hostCallId", "kernelId", "kernelGeneration"} {
			delete(message, key)
		}
		committed, _ := live.observe(message)
		restored, _ := replay.observe(message)
		after := live.completeLive(agentruntime.ToolCall{ID: callID, Name: "mcp__fixture__search"}, value)
		if *after != *committed || *after != *restored || after.RepeatedOperations != i {
			t.Fatalf("direct double completion/drift %d: before=%+v replay=%+v after=%+v", i, committed, restored, after)
		}
	}
}

func TestRunnerSourceObservationProgressUsesSourceAndExecutionContracts(t *testing.T) {
	result := map[string]any{"ok": true, "stdout": "fresh variable value", "files_written": []any{}}
	unknown := &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown, HasSources: true}
	run := &sessionRunnerChatRun{NoProgressRecovery: &sessionRunnerNoProgressRecovery{SourceRepetition: true}, sourceObservationResultCallID: "cell", sourceObservationResult: unknown}
	if !run.completedSourceToolProgress("cell", "repl", result) || !runnerSourceAwareToolProgress(unknown, false, "repl", result) {
		t.Fatal("source mode swallowed ordinary new non-source computation")
	}
	run.observeExecutionEvidence("repl", result)
	repeatedPresentation := run.observeExecutionEvidence("repl", result)
	if runnerGenerationToolProgress("repl", repeatedPresentation) {
		t.Fatal("fixture did not preserve legacy repeated-execution observation")
	}
	fresh := &agentruntime.SourceObservation{State: agentruntime.SourceObservationNew, HasSources: true}
	run.sourceObservationResult = fresh
	if !run.completedSourceToolProgress("cell", "repl", repeatedPresentation) || !runnerSourceAwareToolProgress(fresh, false, "repl", repeatedPresentation) {
		t.Fatal("new verified source was vetoed by unchanged stdout")
	}
	repeated := &agentruntime.SourceObservation{State: agentruntime.SourceObservationRepeated, HasSources: true, RepeatedOperations: 1}
	if runnerSourceAwareToolProgress(repeated, false, "repl", result) {
		t.Fatal("cosmetic wrapper output became new source progress")
	}
}

func TestRunnerSourceObservationRepairScopePreservesInputAdmission(t *testing.T) {
	state := newSessionRunnerNoProgressRecovery(strings.Repeat("a", 64))
	state.sources.observeEntry(eventjournal.Entry{SourceEventType: "user_message"})
	state.sources.observeEntry(eventjournal.Entry{EventID: 11, Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": "first"}}}})
	source := sourceObservationReceipt("host-first", "first")
	key, _ := runnerVerifiedSourceIdentity(source)
	state.sources.observeEntry(eventjournal.Entry{Message: source, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: 11}})
	state.sources.observeEntry(eventjournal.Entry{Message: sourceObservationOuter("first", "same", nil), RuntimeProjection: runnerSourceObservationWitness{proposalEventID: 11, executionResult: sourceObservationOuter("first", "same", nil)["toolResult"]}})
	state.resetScope(strings.Repeat("b", 64))
	if !state.sources.inputScoped {
		t.Fatal("repair scope erased immutable input authority")
	}
	state.sources.observeEntry(eventjournal.Entry{EventID: 22, Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": "second"}}}})
	state.sources.observeEntry(eventjournal.Entry{Message: sourceObservationReceipt("host-second", "second"), RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: 22}})
	got, _ := state.sources.observeEntry(eventjournal.Entry{Message: sourceObservationOuter("second", "same", nil), RuntimeProjection: runnerSourceObservationWitness{proposalEventID: 22, executionResult: sourceObservationOuter("second", "same", nil)["toolResult"]}})
	if got.State != agentruntime.SourceObservationRepeated || got.RepeatedOperations != 1 {
		t.Fatalf("repair scope invented fresh source content: %+v", got)
	}
}

func TestRunnerSourceObservationWithoutAdmittedInputStaysUnknown(t *testing.T) {
	for _, historyOnly := range []bool{false, true} {
		var reducer runnerSourceObservationReducer
		if historyOnly {
			reducer.observeEntry(eventjournal.Entry{SourceEventType: "history_user_message", Message: eventjournal.Message{"role": "user"}})
		}
		for i := 0; i < 2; i++ {
			callID := fmt.Sprint("unanchored-", i)
			reducer.observeEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: sourceObservationReceipt("host-"+callID, callID)})
			got, _ := reducer.observeEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: sourceObservationOuter(callID, "same", nil)})
			if got.State != agentruntime.SourceObservationUnknown || got.RepeatedOperations != 0 {
				t.Fatalf("unanchored raw receipt gained source authority: %+v", got)
			}
		}
	}
}

func TestRunnerSourceObservationNewSourceReopensDurableRecoveryWithReusedOuter(t *testing.T) {
	state := newSessionRunnerNoProgressRecovery(runnerRecoveryObligationFingerprint(nil, transcriptstore.RunnerInterruptionCause{}))
	state.observeEntry(eventjournal.Entry{SourceEventType: "user_message", Message: eventjournal.Message{"type": "message", "role": "user"}}, nil)
	state.recordNoProgress(nil, true)
	state.recordNoProgress(nil, true)
	proposal := int64(10)
	callID := "replayed-new-source"
	state.observeEntry(eventjournal.Entry{EventID: proposal, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": callID}}}}, nil)
	source := sourceObservationReceipt("host-"+callID, callID)
	key, valid := runnerVerifiedSourceIdentity(source)
	if !valid {
		t.Fatal("fixture did not create verified source content")
	}
	state.observeEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: source, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: proposal}}, nil)
	if state.Consecutive != 2 || !state.SourceRepetition {
		t.Fatal("nested source receipt alone reopened the surrounding operation")
	}
	outer := sourceObservationOuter(callID, "same presentation", map[string]any{"reused": true})
	if runnerCheckpointHasMaterialProgress(outer) {
		t.Fatal("fixture did not reproduce a reused completed operation")
	}
	state.observeEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: outer, RuntimeProjection: runnerSourceObservationWitness{proposalEventID: proposal, executionResult: outer["toolResult"]}}, nil)
	if state.Consecutive != 0 || state.SourceRepetition || !state.lastMaterialProgress {
		t.Fatalf("new verified source did not reopen durable recovery: %+v", state)
	}
}

func TestRunnerSourceObservationUnavailableCanonicalOuterOverridesLiveCache(t *testing.T) {
	var live, replay runnerSourceObservationReducer
	input := eventjournal.Entry{SourceEventType: "user_message"}
	live.observeEntry(input)
	replay.observeEntry(input)
	for i := 0; i < 2; i++ {
		proposal := int64(10 + i)
		callID := fmt.Sprint("unavailable-outer-", i)
		header := eventjournal.Entry{EventID: proposal, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": callID}}}}
		source := sourceObservationReceipt("host-"+callID, callID)
		key, _ := runnerVerifiedSourceIdentity(source)
		sourceEntry := eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: source, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: proposal}}
		outer := sourceObservationOuter(callID, "same presentation", nil)
		for _, entry := range []eventjournal.Entry{header, sourceEntry} {
			live.observeEntry(entry)
			replay.observeEntry(entry)
		}
		call := agentruntime.ToolCall{ID: callID, Name: "repl"}
		before := live.completeLive(call, outer["toolResult"])
		witness := runnerSourceObservationWitness{proposalEventID: proposal, executionResult: outer["toolResult"]}
		if i == 1 {
			if before.State != agentruntime.SourceObservationRepeated || before.RepeatedOperations != 1 {
				t.Fatalf("fixture did not create a cached repeated observation: %+v", before)
			}
			witness.executionResult, witness.executionUnavailable = nil, true
		}
		terminal := eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: outer, RuntimeProjection: witness}
		canonical, _ := live.observeEntry(terminal)
		restored, _ := replay.observeEntry(terminal)
		after := live.completeLive(call, outer["toolResult"])
		if *canonical != *restored || *after != *restored {
			t.Fatalf("unavailable canonical result retained stale live state: canonical=%+v replay=%+v live=%+v", canonical, restored, after)
		}
		if i == 1 && (after.State != agentruntime.SourceObservationUnknown || after.RepeatedOperations != 0 || live.admitted[callID] != 0 || live.liveComputed) {
			t.Fatalf("canonical unavailability did not finalize exactly once: %+v", after)
		}
	}
}
