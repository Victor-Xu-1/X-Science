package server

import (
	"encoding/json"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerSourceReplayKeepsOrdinaryClosedRoutesUntilActualProgress(t *testing.T) {
	for _, test := range []struct {
		name       string
		newSource  bool
		material   bool
		wantClosed bool
	}{
		{"repeated source", false, false, true},
		{"new verified source", true, false, false},
		{"material output", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newSessionRunnerNoProgressRecovery(runnerRecoveryObligationFingerprint(nil, transcriptstore.RunnerInterruptionCause{}))
			entries := []eventjournal.Entry{}
			appendEntry := func(entry eventjournal.Entry) {
				entries = append(entries, entry)
				state.observeEntry(entry, nil)
			}
			appendEntry(eventjournal.Entry{SourceEventType: "user_message", Message: eventjournal.Message{"type": "message", "role": "user"}})
			appendStep := func(proposal int64, callID string, fresh, material bool) {
				appendEntry(eventjournal.Entry{EventID: proposal, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{"id": callID}}}})
				source := sourceObservationReceipt("host-"+callID, callID)
				if fresh {
					source["toolInput"] = map[string]any{"query": "a different controlled query", "page": 1}
					raw, _ := json.Marshal(source["toolInput"])
					source["requestSha256"] = kernelMCPEvidenceSHA256(raw)
				}
				key, valid := runnerVerifiedSourceIdentity(source)
				if !valid {
					t.Fatal("fixture source identity is not verified")
				}
				appendEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: source, RuntimeProjection: runnerSourceObservationWitness{key: key, proposalEventID: proposal}})
				extra := map[string]any{}
				if material {
					extra["files_written"] = []any{"new-result.dat"}
				}
				outer := sourceObservationOuter(callID, "same source presentation", extra)
				appendEntry(eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: outer, RuntimeProjection: runnerSourceObservationWitness{proposalEventID: proposal, executionResult: outer["toolResult"]}})
			}
			appendStep(10, "first", false, false)
			closedCall := agentruntime.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"existing.txt"}`)}
			fingerprint := agentruntime.ExecutionCallFingerprint(closedCall.Name, closedCall.Arguments)
			state.recordNoProgress([]agentruntime.ToolCall{closedCall})
			entries = append(entries, eventjournal.Entry{SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
				"type": "runner_checkpoint", "status": "interrupted", "reason_code": sessionRunnerToolRoundNoProgressReasonCode,
				"resume_detail": state.resumeDetail("No material progress."), "recovery_contract_revision": sessionRunnerRecoveryContractRevision,
			}})
			appendStep(20, "second", test.newSource, test.material)
			restored := runnerNoProgressRecoveryFromEntries(entries, nil)
			for label, actual := range map[string]sessionRunnerNoProgressRecovery{"observed": state, "replayed": restored} {
				if actual.containsFingerprint(fingerprint) != test.wantClosed || actual.lastMaterialProgress == test.wantClosed || actual.SourceRepetition {
					t.Fatalf("%s recovery drifted without material evidence: %+v", label, actual)
				}
			}
		})
	}
}
