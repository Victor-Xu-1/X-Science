package server

import (
	"encoding/json"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	"x-science/internal/toolcontract"
)

// One reduction serves live completion and canonical replay. It observes source
// content, never guesses effects by parsing code or comparing interpreter text.
type runnerSourceObservationReducer struct {
	window               agentruntime.SourceObservationWindow
	liveCallID           string
	liveObservation      agentruntime.SourceObservation
	liveComputed         bool
	completedCallID      string
	completedObservation agentruntime.SourceObservation
	inputScoped          bool
	admitted             map[string]int64
}

func (reducer *runnerSourceObservationReducer) recordCompleted(callID string, observation agentruntime.SourceObservation) *agentruntime.SourceObservation {
	reducer.completedCallID, reducer.completedObservation = callID, observation
	if reducer.liveCallID == callID {
		reducer.liveCallID = ""
		reducer.liveComputed = false
	}
	delete(reducer.admitted, callID)
	return &observation
}

func runnerVerifiedSourceIdentity(message eventjournal.Message, originalDigest ...string) (string, bool) {
	raw, err := json.Marshal(message)
	if err != nil {
		return "", false
	}
	var checkpoint sessionRunnerDurableToolCheckpoint
	if json.Unmarshal(raw, &checkpoint) != nil || !validSessionRunnerMCPDurableCheckpoint(checkpoint) {
		return "", false
	}
	resultDigest := checkpoint.ResultSHA256
	if _, _, externalized, _ := toolcontract.DecodeExternalizedResult(checkpoint.ToolResult); externalized {
		if len(originalDigest) == 0 {
			return "", false
		}
		resultDigest = originalDigest[0]
	}
	return (agentruntime.SourceEvidenceIdentity{
		Class: checkpoint.EvidenceClass, ConnectorID: checkpoint.ConnectorID,
		ConnectorSource: checkpoint.ConnectorSource, ToolName: checkpoint.ToolName,
		InputSchemaSHA256: checkpoint.InputSchemaSHA256,
		RequestSHA256:     checkpoint.RequestSHA256, ResultSHA256: resultDigest,
	}).Key()
}

func runnerSourceMaterialChange(name string, value any) bool {
	result := mapValue(value)
	if agentruntime.ToolResultDidNotExecute(result) || agentruntime.ClassifyToolResult(result).HardFailed() {
		return false
	}
	if agentruntime.ToolResultReportsMaterialProgress(result) {
		return runnerToolCompletionHasMaterialProgress(name, result)
	}
	if !isAgentKernelToolName(name) {
		return false
	}
	for _, key := range []string{"files_written", "dropped_roots", "artifacts"} {
		if raw, err := json.Marshal(result[key]); err == nil && string(raw) != "null" && string(raw) != "[]" {
			return true
		}
	}
	return false
}

// sourceCheckpoint distinguishes a nested source receipt from completion of
// its surrounding cell. That receipt alone must not reset a source-mode budget.
func (reducer *runnerSourceObservationReducer) observe(message eventjournal.Message, witnesses ...runnerSourceObservationWitness) (observation *agentruntime.SourceObservation, sourceCheckpoint bool) {
	if message["type"] != "runner_checkpoint" || message["status"] != "completed" || message["toolPhase"] != "completed" {
		return nil, false
	}
	callID := strings.TrimSpace(stringValue(message["toolCallId"]))
	if callID == "" {
		return nil, false
	}
	schema := stringValue(message["schema"])
	if schema == "x-science.kernel_mcp_evidence.v1" || schema == workspaceMCPSourceEvidenceSchemaV1 {
		operation := firstNonEmpty(stringValue(message["outerToolCallId"]), callID)
		key, verified := runnerVerifiedSourceIdentity(message)
		if len(witnesses) > 0 {
			key, verified = witnesses[0].key, witnesses[0].key != ""
		}
		verified = verified && runnerSourceReceiptSucceeded(message["toolResult"])
		if reducer.inputScoped && reducer.admitted[operation] == 0 {
			verified = false
		}
		if reducer.inputScoped && len(witnesses) == 0 {
			verified = false
		}
		if verified {
			reducer.window.Observe(operation, key)
		} else {
			reducer.window.Unknown(operation)
		}
		if schema == "x-science.kernel_mcp_evidence.v1" {
			return nil, true
		}
		result := reducer.window.Complete(operation, false)
		return reducer.recordCompleted(callID, result), true
	}
	if reducer.liveCallID == callID && reducer.liveComputed {
		result := reducer.liveObservation
		return reducer.recordCompleted(callID, result), false
	}
	if result := message["toolResult"]; agentruntime.ToolResultDidNotExecute(result) || agentruntime.ClassifyToolResult(result).HardFailed() {
		reducer.window.Unknown(callID)
	}
	result := reducer.window.Complete(callID, runnerSourceMaterialChange(stringValue(message["toolName"]), message["toolResult"]))
	return reducer.recordCompleted(callID, result), false
}

func (reducer *runnerSourceObservationReducer) observeEntry(entry eventjournal.Entry) (*agentruntime.SourceObservation, bool) {
	if entry.SourceEventType == "user_message" || entry.SourceEventType == "user_input_response" {
		*reducer = runnerSourceObservationReducer{inputScoped: true, admitted: map[string]int64{}}
		return nil, false
	}
	if !reducer.inputScoped {
		if entry.Message["schema"] == "x-science.kernel_mcp_evidence.v1" || entry.Message["schema"] == workspaceMCPSourceEvidenceSchemaV1 {
			return nil, true
		}
		if entry.Message["toolPhase"] == "completed" {
			return &agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}, false
		}
		return nil, false
	}
	if reducer.inputScoped && entry.Message["type"] == "runner_checkpoint" {
		if calls, found := entry.Message["modelToolCalls"].([]any); found {
			for _, raw := range calls {
				call := mapValue(raw)
				if id := stringValue(call["id"]); id != "" && len(reducer.admitted) < 256 && call["rejectedBeforeExecution"] != true {
					reducer.window.DiscardPending(id)
					reducer.admitted[id] = entry.EventID
					if reducer.completedCallID == id {
						reducer.completedCallID = ""
					}
					if reducer.liveCallID == id {
						reducer.liveCallID = ""
						reducer.liveComputed = false
					}
				}
			}
		}
	}
	if witness, found := entry.RuntimeProjection.(runnerSourceObservationWitness); found {
		operation := firstNonEmpty(stringValue(entry.Message["outerToolCallId"]), stringValue(entry.Message["toolCallId"]))
		if reducer.inputScoped && (witness.proposalEventID == 0 || reducer.admitted[operation] != witness.proposalEventID) {
			return nil, true
		}
		if witness.executionUnavailable {
			operation := stringValue(entry.Message["toolCallId"])
			reducer.window.Unknown(operation)
			result := reducer.window.Complete(operation, false)
			return reducer.recordCompleted(operation, result), false
		}
		if witness.executionResult != nil {
			message := copyMapAny(entry.Message)
			message["toolResult"] = witness.executionResult
			return reducer.observe(message)
		}
		return reducer.observe(entry.Message, witness)
	}
	return reducer.observe(entry.Message)
}

func runnerSourceObservationResult(entry eventjournal.Entry) any {
	if witness, found := entry.RuntimeProjection.(runnerSourceObservationWitness); found && witness.executionResult != nil {
		return witness.executionResult
	}
	return entry.Message["toolResult"]
}

func (reducer *runnerSourceObservationReducer) completeLive(call agentruntime.ToolCall, value any) *agentruntime.SourceObservation {
	if reducer.completedCallID == call.ID {
		result := reducer.completedObservation
		return &result
	}
	if reducer.liveCallID == call.ID {
		result := reducer.liveObservation
		return &result
	}
	if agentruntime.ToolResultDidNotExecute(value) || agentruntime.ClassifyToolResult(value).HardFailed() {
		reducer.window.Unknown(call.ID)
	}
	observation := agentruntime.SourceObservation{State: agentruntime.SourceObservationUnknown}
	computed := reducer.window.HasPending(call.ID) || runnerSourceMaterialChange(call.Name, value)
	if computed {
		observation = reducer.window.Complete(call.ID, runnerSourceMaterialChange(call.Name, value))
	}
	reducer.liveCallID, reducer.liveObservation = call.ID, observation
	reducer.liveComputed = computed
	return &observation
}

func (run *sessionRunnerChatRun) completedSourceToolProgress(callID, name string, result any) bool {
	if run == nil {
		return runnerGenerationToolProgress(name, result)
	}
	run.sourceObservationMu.Lock()
	defer run.sourceObservationMu.Unlock()
	if run.sourceObservationResultCallID == callID && run.sourceObservationResult != nil {
		return runnerSourceAwareToolProgress(run.sourceObservationResult, false, name, result)
	}
	return runnerGenerationToolProgress(name, result)
}

func runnerSourceAwareToolProgress(observation *agentruntime.SourceObservation, sourceCheckpoint bool, name string, result any) bool {
	if observation != nil && observation.State == agentruntime.SourceObservationNew {
		return true
	}
	if sourceObservationIsUnchanged(observation, sourceCheckpoint) && !runnerSourceMaterialChange(name, result) {
		return false
	}
	return runnerGenerationToolProgress(name, result)
}
