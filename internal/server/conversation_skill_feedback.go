package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	conversationSkillFeedbackMaxBytes   = 64 << 10
	conversationSkillFeedbackMaxRecords = 128
	conversationSkillFeedbackMaxSources = 512
)

// This is bounded recorded data for the existing authoring continuation, not
// an execution permission, a causal/quality score, or publication authority.
type conversationSkillExecutionFeedback struct {
	Loads        []conversationSkillFeedbackRecord `json:"loads"`
	ToolOutcomes []conversationSkillFeedbackRecord `json:"tool_outcomes"`
	UserFeedback []conversationSkillFeedbackRecord `json:"user_feedback"`
	Omitted      int                               `json:"omitted"`
}

type conversationSkillFeedbackRecord struct {
	Kind                    string                                   `json:"kind"`
	IdentityStatus          string                                   `json:"identity_status,omitempty"`
	IdentityUnknownReason   string                                   `json:"identity_unknown_reason,omitempty"`
	EventID                 int64                                    `json:"event_id"`
	PublicationSequence     int64                                    `json:"publication_sequence"`
	RunnerAttempt           int64                                    `json:"runner_attempt,omitempty"`
	RequestMessageID        string                                   `json:"request_message_id,omitempty"`
	InputRevision           int64                                    `json:"input_revision,omitempty"`
	SourceEventID           int64                                    `json:"source_event_id,omitempty"`
	SourceRunnerAttempt     int64                                    `json:"source_runner_attempt,omitempty"`
	SourceRequestMessageID  string                                   `json:"source_request_message_id,omitempty"`
	SourceInputRevision     int64                                    `json:"source_input_revision,omitempty"`
	ToolCallID              string                                   `json:"tool_call_id,omitempty"`
	ToolName                string                                   `json:"tool_name,omitempty"`
	ToolPhase               string                                   `json:"tool_phase,omitempty"`
	Status                  string                                   `json:"status,omitempty"`
	ToolInput               json.RawMessage                          `json:"tool_input,omitempty"`
	ExecutedToolInput       json.RawMessage                          `json:"executed_tool_input,omitempty"`
	ToolResult              json.RawMessage                          `json:"tool_result,omitempty"`
	ResultOutcome           agentruntime.ToolResultOutcome           `json:"result_outcome,omitempty"`
	CanonicalOutcome        *transcriptstore.RunnerReplayToolOutcome `json:"canonical_outcome,omitempty"`
	Receipt                 *agentruntime.SkillLoadReceipt           `json:"receipt,omitempty"`
	PriorLoadInvocationKeys []string                                 `json:"prior_load_invocation_keys,omitempty"`
	PriorLoadEventIDs       []int64                                  `json:"prior_load_event_ids,omitempty"`
	Text                    string                                   `json:"text,omitempty"`
	OmittedFields           []string                                 `json:"omitted_fields,omitempty"`
}

type conversationSkillFeedbackSource struct {
	eventID, publication, attempt int64
	requestID                     string
}

type conversationSkillFeedbackItem struct {
	record conversationSkillFeedbackRecord
	bytes  int
}

type conversationSkillFeedbackAccumulator struct {
	server        *Server
	authority     *transcriptRunnerAuthority
	records       []conversationSkillFeedbackItem
	recordBytes   int
	omitted       int
	sources       map[int64]conversationSkillFeedbackSource
	sourceOrder   []int64
	runtimeStates map[int64]transcriptstore.RunnerRuntimeState
	loadsByInput  map[string]map[string]int64
	currentInput  string
}

func newConversationSkillFeedbackAccumulator(s *Server, authority *transcriptRunnerAuthority) *conversationSkillFeedbackAccumulator {
	return &conversationSkillFeedbackAccumulator{
		server: s, authority: authority, sources: make(map[int64]conversationSkillFeedbackSource),
		runtimeStates: make(map[int64]transcriptstore.RunnerRuntimeState), loadsByInput: make(map[string]map[string]int64),
	}
}

// Observe each selected original event before prose trimming. Source admission
// and terminal execution may belong to different attempts after recovery.
func (a *conversationSkillFeedbackAccumulator) observe(ctx context.Context, projected transcriptstore.ProjectedEvent, payload eventjournal.Message, input *transcriptstore.ClaimedInputContext) error {
	event := projected.Event
	if event.Type == "user_message" || event.Type == "history_user_message" {
		if stringValue(payload["messageOrigin"]) != "input_response" && !isHiddenConversationControlPayload(payload) {
			if keys, events := a.loadReferences(a.currentInput); len(keys) > 0 {
				a.append(conversationSkillFeedbackRecord{
					Kind: "subsequent_user_feedback", EventID: event.EventID, PublicationSequence: event.PublicationSeq,
					RequestMessageID: event.ClientMessageID, PriorLoadInvocationKeys: keys, PriorLoadEventIDs: events,
					Text: truncateConversationSkillText(runnerMessageText(payload), 2048),
				})
			}
			a.currentInput = event.ClientMessageID
		}
		return nil
	}
	if event.RunnerAttempt == nil {
		return nil
	}
	if calls, _ := payload["modelToolCalls"].([]any); len(calls) > 0 && input != nil {
		a.sources[event.EventID] = conversationSkillFeedbackSource{eventID: event.EventID, publication: event.PublicationSeq,
			attempt: *event.RunnerAttempt, requestID: input.ClientMessageID}
		a.sourceOrder = append(a.sourceOrder, event.EventID)
		if len(a.sourceOrder) > conversationSkillFeedbackMaxSources {
			delete(a.sources, a.sourceOrder[0])
			a.sourceOrder = a.sourceOrder[1:]
			a.omitted++
		}
	}
	phase, callID := stringValue(payload["toolPhase"]), stringValue(payload["toolCallId"])
	if callID == "" || !sessionRunnerToolContinuityTerminalPhase(phase) ||
		(event.Type != "runner_checkpoint" && event.Type != transcriptstore.TerminalToolRecoveryEventType) {
		return nil
	}
	record := conversationSkillFeedbackRecord{
		EventID: event.EventID, PublicationSequence: event.PublicationSeq, RunnerAttempt: *event.RunnerAttempt,
		ToolCallID: callID, ToolName: stringValue(payload["toolName"]), ToolPhase: phase, Status: stringValue(payload["status"]),
	}
	if input != nil {
		record.RequestMessageID = input.ClientMessageID
	}
	if state, found := a.runtimeStates[record.RunnerAttempt]; found {
		record.InputRevision = state.ClaimedInputRevision
	}
	var source conversationSkillFeedbackSource
	if outcome := projected.ToolOutcome; outcome != nil && outcome.TerminalEventID == event.EventID &&
		outcome.ToolCallID == callID && outcome.SourceEventID > 0 && outcome.SourceEventID < event.EventID {
		copy := *outcome
		record.CanonicalOutcome = &copy
		record.SourceEventID = outcome.SourceEventID
		source = a.sources[outcome.SourceEventID]
		if source.eventID > 0 && source.publication < event.PublicationSeq {
			record.SourceRunnerAttempt, record.SourceRequestMessageID = source.attempt, source.requestID
			if state, found := a.runtimeStates[source.attempt]; found {
				record.SourceInputRevision = state.ClaimedInputRevision
			}
			record.PriorLoadInvocationKeys, record.PriorLoadEventIDs = a.loadReferences(source.requestID)
		}
	}
	for _, field := range []struct {
		name   string
		target *json.RawMessage
	}{{"toolInput", &record.ToolInput}, {"executedToolInput", &record.ExecutedToolInput}} {
		if value, present := payload[field.name]; present {
			*field.target = boundedConversationSkillFeedbackJSON(value, 2048)
			if len(*field.target) == 0 {
				record.OmittedFields = append(record.OmittedFields, field.name)
			}
		}
	}
	if value, present := payload["toolResult"]; present {
		record.ResultOutcome = agentruntime.ClassifyToolResult(value)
		record.ToolResult = boundedConversationSkillFeedbackJSON(value, 2048)
		if len(record.ToolResult) == 0 {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			diagnostics, err := conversationSkillFeedbackDiagnostics(value, true)
			if err != nil {
				return err
			}
			record.ToolResult, err = json.Marshal(map[string]any{"truncated": true, "byte_length": len(raw),
				"preview":     truncateConversationSkillText(string(raw), 1024),
				"diagnostics": diagnostics})
			if err != nil {
				return err
			}
			record.OmittedFields = append(record.OmittedFields, "toolResult")
		}
	}
	if isCompletedSkillToolName(record.ToolName) {
		record.Kind, record.IdentityStatus = "skill_load", "identity_unknown"
		record.IdentityUnknownReason = "no_verified_host_load_and_canonical_source"
		if err := a.verifyLoad(ctx, event, payload, input, source, &record); err != nil {
			return err
		}
		a.append(record)
	} else if record.CanonicalOutcome != nil {
		record.Kind = "tool_terminal"
		a.append(record)
	}
	return nil
}

func (a *conversationSkillFeedbackAccumulator) verifyLoad(ctx context.Context, event transcriptstore.Event, payload eventjournal.Message, input *transcriptstore.ClaimedInputContext, source conversationSkillFeedbackSource, record *conversationSkillFeedbackRecord) error {
	if record.CanonicalOutcome == nil || source.eventID == 0 || source.requestID == "" || input == nil ||
		source.attempt > record.RunnerAttempt {
		return nil
	}
	payload["type"], payload["runnerAttempt"] = event.Type, record.RunnerAttempt
	receipt := runtimeSkillLoadReceiptFromCheckpoint(eventjournal.Entry{Message: payload})
	if !runtimeSkillLoadReceiptForScope(receipt, a.authority, record.ToolCallID) {
		return nil
	}
	for _, attempt := range []int64{source.attempt, record.RunnerAttempt} {
		if _, found := a.runtimeStates[attempt]; found {
			continue
		}
		state, err := a.server.transcriptStore.GetRunnerRuntimeState(ctx, a.authority.Stream.UID, a.authority.Stream.OwnerID, attempt)
		if errors.Is(err, sql.ErrNoRows) {
			a.runtimeStates[attempt] = transcriptstore.RunnerRuntimeState{}
			continue // Missing historical evidence is unknown, not a Save failure.
		}
		if err != nil {
			return err
		}
		a.runtimeStates[attempt] = state
	}
	sourceState, terminalState := a.runtimeStates[source.attempt], a.runtimeStates[record.RunnerAttempt]
	if receipt.Binding.InputRevision != terminalState.ClaimedInputRevision ||
		sourceState.ClaimedInputRevision <= 0 || terminalState.ClaimedInputRevision <= 0 {
		return nil
	}
	record.InputRevision, record.SourceInputRevision = terminalState.ClaimedInputRevision, sourceState.ClaimedInputRevision
	record.Receipt, record.IdentityStatus, record.IdentityUnknownReason = receipt.Clone(), "proven", ""
	if a.loadsByInput[source.requestID] == nil {
		a.loadsByInput[source.requestID] = make(map[string]int64)
	}
	a.loadsByInput[source.requestID][receipt.InvocationKey] = event.EventID
	return nil
}

func boundedConversationSkillFeedbackJSON(value any, maxBytes int) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxBytes {
		return nil
	}
	return raw
}

// A large successful-looking lifecycle envelope must not hide an error behind
// its data preview. Preserve only bounded original diagnostic fields from the
// same envelope levels inspected by the existing outcome classifier.
func conversationSkillFeedbackDiagnostics(value any, includeNested bool) (map[string]any, error) {
	envelope, _ := value.(map[string]any)
	diagnostics := make(map[string]any)
	for _, field := range []string{
		"error", "failure", "errors", "message", "code", "status", "stopReason", "stop_reason",
		"sourceUnavailable", "sources", "ok", "success", "isError", "partial", "executed", "preflight",
	} {
		if original, present := envelope[field]; present {
			raw, err := json.Marshal(original)
			if err != nil {
				return nil, err
			}
			if len(raw) <= 512 {
				diagnostics[field] = json.RawMessage(raw)
			} else {
				diagnostics[field] = map[string]any{"truncated": true, "preview": truncateConversationSkillText(string(raw), 384)}
			}
		}
	}
	if includeNested {
		nested, err := conversationSkillFeedbackDiagnostics(envelope["result"], false)
		if err != nil {
			return nil, err
		}
		if len(nested) > 0 {
			diagnostics["result"] = nested
		}
	}
	return diagnostics, nil
}

func (a *conversationSkillFeedbackAccumulator) loadReferences(inputID string) ([]string, []int64) {
	var keys []string
	for key := range a.loadsByInput[inputID] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	events := make([]int64, len(keys))
	for index, key := range keys {
		events[index] = a.loadsByInput[inputID][key]
	}
	return keys, events
}

func (a *conversationSkillFeedbackAccumulator) append(record conversationSkillFeedbackRecord) {
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > conversationSkillFeedbackMaxBytes-512 {
		a.omitted++ // A receipt is either present in full or explicitly omitted.
		if record.Receipt != nil {
			a.removeLoad(record.Receipt.InvocationKey, record.EventID)
		}
		return
	}
	a.records = append(a.records, conversationSkillFeedbackItem{record: record, bytes: len(raw)})
	a.recordBytes += len(raw)
	for len(a.records) > conversationSkillFeedbackMaxRecords || a.recordBytes > conversationSkillFeedbackMaxBytes-512 {
		latestOutcome, latestFeedback := -1, -1
		for index, item := range a.records {
			if item.record.Kind == "tool_terminal" {
				latestOutcome = index
			} else if item.record.Kind == "subsequent_user_feedback" {
				latestFeedback = index
			}
		}
		remove := -1
		for index, item := range a.records {
			if item.record.Receipt == nil && index != len(a.records)-1 && index != latestOutcome && index != latestFeedback {
				remove = index
				break
			}
		}
		if remove < 0 {
			// Repeated loads cannot starve the latest real outcome or ordinary
			// feedback. Keep recent complete loads and observations together.
			for index := 0; index < len(a.records)-1; index++ {
				if a.records[index].record.Receipt != nil {
					remove = index
					break
				}
			}
		}
		if remove < 0 {
			remove = 0
		}
		removed := a.records[remove]
		a.recordBytes -= removed.bytes
		a.records = append(a.records[:remove], a.records[remove+1:]...)
		a.omitted++
		if removed.record.Receipt != nil {
			a.removeLoad(removed.record.Receipt.InvocationKey, removed.record.EventID)
		}
	}
}

func (a *conversationSkillFeedbackAccumulator) removeLoad(key string, eventID int64) {
	// A removed full load must not leave a hidden active identity available for
	// later attribution. Other independently retained loads remain a set.
	for input, keys := range a.loadsByInput {
		if keys[key] == eventID {
			delete(keys, key)
		}
		if len(keys) == 0 {
			delete(a.loadsByInput, input)
		}
	}
}

func (a *conversationSkillFeedbackAccumulator) snapshot() (conversationSkillExecutionFeedback, string, error) {
	feedback := conversationSkillExecutionFeedback{Omitted: a.omitted}
	retainedLoads := make(map[int64]string)
	for _, item := range a.records {
		if item.record.Receipt != nil {
			retainedLoads[item.record.EventID] = item.record.Receipt.InvocationKey
		}
	}
	for _, item := range a.records {
		record := item.record
		record.PriorLoadInvocationKeys = nil
		record.PriorLoadEventIDs = nil
		for index, key := range item.record.PriorLoadInvocationKeys {
			if index < len(item.record.PriorLoadEventIDs) && retainedLoads[item.record.PriorLoadEventIDs[index]] == key {
				record.PriorLoadInvocationKeys = append(record.PriorLoadInvocationKeys, key)
				record.PriorLoadEventIDs = append(record.PriorLoadEventIDs, item.record.PriorLoadEventIDs[index])
			}
		}
		switch record.Kind {
		case "skill_load":
			feedback.Loads = append(feedback.Loads, record)
		case "tool_terminal":
			feedback.ToolOutcomes = append(feedback.ToolOutcomes, record)
		case "subsequent_user_feedback":
			feedback.UserFeedback = append(feedback.UserFeedback, record)
		}
	}
	raw, err := json.Marshal(map[string]any{"skill_execution_feedback": feedback})
	return feedback, strings.TrimSpace(string(raw)), err
}
