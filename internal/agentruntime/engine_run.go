package agentruntime

import (
	"context"
	"errors"
	"strings"
)

func (e Engine) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if e.Model == nil {
		return RunResult{}, errors.New("agent runtime model client is required")
	}
	if len(request.Messages) == 0 {
		return RunResult{}, errors.New("agent runtime requires at least one message")
	}
	normalized, err := NormalizeModelRequestMedia(ModelRequest{
		Messages: request.Messages, MediaPolicy: request.MediaPolicy,
	})
	if err != nil {
		return RunResult{}, err
	}
	maxToolRounds := request.MaxToolRounds
	admissionChecker, prevalidateToolCalls := e.Tools.(ToolCallAdmissionChecker)
	preflightChecker, preflightToolCalls := e.Tools.(ToolCallPreflightDiagnostics)
	toolChoicePolicy, hasToolChoicePolicy := e.Tools.(ModelToolChoicePolicy)
	toolSchemaExpansion, hasToolSchemaExpansion := e.Tools.(ModelToolSchemaExpansion)
	requiredToolRecovery, hasRequiredToolRecovery := e.Tools.(RequiredToolCallRecovery)
	e.Tools = newFailedToolCallGuard(e.Tools, request.Tools...)

	messages := append([]Message(nil), normalized.Messages...)
	modelMessages := append([]Message(nil), messages...)
	mediaBytesUsed := modelMessageMediaBytes(messages)
	toolRounds := 0
	activeTools := appendUniqueToolSchemas(nil, request.Tools...)
	initialConstraint := parseInitialToolConstraint(request.InitialToolChoice)
	toolChoiceRepairs := 0
	consecutivePrivatePreflightRepairs := 0
	rejectionFamilyAttempts := make(map[string]int)
	if initialConstraint.required && len(activeTools) == 0 {
		return RunResult{}, &InitialToolChoiceViolationError{RequiredTool: initialConstraint.name}
	}
	initialTools, initialToolAvailable := initialConstraint.requestTools(activeTools)
	if !initialToolAvailable {
		return RunResult{}, &InitialToolChoiceViolationError{RequiredTool: initialConstraint.name}
	}
	var lastToolCallRoundSignature string
	consecutiveIdenticalToolRounds := 0
	consecutiveNoProgressRounds := 0
	noProgressRecoveryCalls := []ToolCall{}
	for {
		// A cancelled or timed-out run must stop before the next model request
		// so completed tool rounds stay available to the caller. Emitting
		// EventModelRequest after cancellation would invent a request that
		// never happened.
		if err := ctx.Err(); err != nil {
			return interruptedRun(messages, err)
		}
		if err := e.emit(Event{Type: EventModelRequest}); err != nil {
			return interruptedRun(messages, err)
		}
		modelRequest := ModelRequest{Messages: modelMessages, Tools: activeTools, Metadata: request.Metadata, Headers: request.Headers, MediaPolicy: normalized.MediaPolicy}
		toolChoice := any(nil)
		if toolRounds == 0 {
			toolChoice = request.InitialToolChoice
		} else if hasToolChoicePolicy {
			toolChoice = toolChoicePolicy.RequiredToolChoice(modelMessages, activeTools)
		}
		roundConstraint := parseInitialToolConstraint(toolChoice)
		roundTools := activeTools
		if toolRounds == 0 {
			roundConstraint = initialConstraint
			roundTools = initialTools
		} else if roundConstraint.required || roundConstraint.forbidden {
			var available bool
			roundTools, available = roundConstraint.requestTools(activeTools)
			if !available {
				return interruptedRun(messages, &InitialToolChoiceViolationError{RequiredTool: roundConstraint.name})
			}
		}
		modelRequest.ToolChoice = toolChoice
		modelRequest.Tools = roundTools
		enforceToolChoice := roundConstraint.required || roundConstraint.forbidden
		response, bufferedDeltas, err := e.completeModelRound(ctx, modelRequest, enforceToolChoice)
		if err != nil {
			return interruptedRun(messages, err)
		}
		message := response.Message
		preamble, bufferedDeltas, err := e.publishToolPreamble(enforceToolChoice, message, bufferedDeltas)
		if err != nil {
			return interruptedRun(messages, err)
		}
		if enforceToolChoice && !roundConstraint.satisfied(message.ToolCalls) {
			resolution, retry, resolutionErr := resolveToolChoiceViolation(
				roundConstraint, toolChoiceRepairs, requiredToolRecovery, hasRequiredToolRecovery,
				modelMessages, activeTools,
			)
			if resolutionErr != nil {
				return interruptedRun(messages, resolutionErr)
			}
			if retry {
				toolChoiceRepairs++
				messages, modelMessages = preamble.toolChoiceRepairMessages(messages, resolution)
				continue
			}
			message = resolution
			bufferedDeltas = nil
		}
		if enforceToolChoice {
			toolChoiceRepairs = 0
			if err := e.publishBufferedModelDeltas(bufferedDeltas); err != nil {
				return interruptedRun(messages, err)
			}
		}
		if len(message.ToolCalls) > 0 {
			if err := validateToolCallBatch(message.ToolCalls, request.MaxToolCallsPerRound); err != nil {
				return interruptedRun(messages, err)
			}
		}
		var rejectedToolCalls map[int]toolCallRejection
		roundHadNarrative := false
		priorToolRoundSignature := ""
		priorIdenticalToolRounds := 0
		roundSignature := ""
		if len(message.ToolCalls) > 0 {
			message.ToolCalls = applyToolSchemaDefaults(message.ToolCalls, activeTools)
			var admission ToolCallAdmissionChecker
			if prevalidateToolCalls {
				admission = admissionChecker
			}
			var preflight ToolCallPreflightDiagnostics
			if preflightToolCalls {
				preflight = preflightChecker
			}
			message.ToolCalls = namespaceReusedToolCallIDs(modelMessages, message.ToolCalls, toolRounds+1)
			rejectedToolCalls, err = collectToolCallRejections(
				ctx, message.ToolCalls, modelRequest.Tools, admission, preflight,
			)
			if err != nil {
				return interruptedRun(messages, err)
			}
			applyToolCallRejectionFamilyBudget(message.ToolCalls, rejectedToolCalls, rejectionFamilyAttempts)
			if consecutivePrivatePreflightRepairs < maxPrivatePreflightRepairAttempts {
				feedback, repairable, repairErr := privatePreflightRepairMessages(message.ToolCalls, rejectedToolCalls)
				if repairErr != nil {
					return interruptedRun(messages, repairErr)
				}
				if repairable {
					consecutivePrivatePreflightRepairs++
					messages = preamble.preserveForRepair(messages)
					modelMessages = append(modelMessages, message)
					modelMessages = append(modelMessages, feedback...)
					continue
				}
			}
			for index := range rejectedToolCalls {
				message.ToolCalls[index].RejectedBeforeExecution = true
			}
			// Exhaustion closes an invalid proposal family, never a capability.
			// A corrected native proposal still passes its ordinary admission;
			// rejection-only rounds yield through the resumable no-progress path.
			roundHadNarrative = strings.TrimSpace(message.Content) != ""
			priorToolRoundSignature = lastToolCallRoundSignature
			priorIdenticalToolRounds = consecutiveIdenticalToolRounds
			roundSignature = toolCallRoundSignature(message.ToolCalls, rejectedToolCalls)
			rejectionOnly := len(rejectedToolCalls) > 0 && len(rejectedToolCalls) == len(message.ToolCalls)
			if !roundHadNarrative || rejectionOnly {
				signature := roundSignature
				if signature != "" && signature == lastToolCallRoundSignature {
					consecutiveIdenticalToolRounds++
				} else {
					consecutiveIdenticalToolRounds = 1
				}
				lastToolCallRoundSignature = signature
				if request.MaxConsecutiveIdenticalToolRounds > 0 &&
					consecutiveIdenticalToolRounds >= request.MaxConsecutiveIdenticalToolRounds {
					return interruptedRun(messages, &ToolRoundNoProgressError{
						Limit: request.MaxConsecutiveIdenticalToolRounds,
						Calls: appendNoProgressRecoveryCalls(noProgressRecoveryCalls, message.ToolCalls),
					})
				}
			} else {
				lastToolCallRoundSignature = ""
				consecutiveIdenticalToolRounds = 0
			}
			if request.ToolRoundBudget != nil {
				if err := request.ToolRoundBudget.consume(); err != nil {
					return interruptedRun(messages, err)
				}
			} else if maxToolRounds > 0 && toolRounds >= maxToolRounds {
				return interruptedRun(messages, &ToolRoundLimitError{Limit: maxToolRounds})
			}
		}
		if err := e.emit(Event{Type: EventModelResponse, Message: message.Content, ToolCalls: append([]ToolCall(nil), message.ToolCalls...)}); err != nil {
			return interruptedRun(messages, err)
		}
		if len(message.ToolCalls) == 0 {
			return e.finishRun(messages, message)
		}
		messages = append(messages, message)
		var batch ToolBatchExecution
		var toolErr error
		if len(rejectedToolCalls) > 0 {
			batch, toolErr = e.executeToolCallRoundWithRejections(
				ctx, message.ToolCalls, rejectedToolCalls, normalized.MediaPolicy, mediaBytesUsed,
			)
		} else {
			batch, toolErr = e.ExecuteToolBatch(ctx, message.ToolCalls, 0, normalized.MediaPolicy, mediaBytesUsed)
		}
		messages = append(messages, batch.Messages...)
		// Admission alone proves no effect. Reset the private repair window
		// only after actual progress; retain per-family history until a valid
		// call to that same operation target succeeds.
		if !batch.NoProgress {
			consecutivePrivatePreflightRepairs = 0
			if toolRoundReportsMaterialProgress(batch.Messages) {
				// A host-confirmed effect reopens an identical continuation. A
				// bare successful status alone cannot keep a repeated noop alive.
				lastToolCallRoundSignature = ""
				consecutiveIdenticalToolRounds = 0
			}
			clearResolvedToolRejectionFamilies(message.ToolCalls, batch.Messages, rejectionFamilyAttempts)
		}
		// A reused idempotent read is not new evidence. Count consecutive
		// repeated read rounds even when the model adds a short narrative around
		// each call; otherwise a weak model can evade the ordinary tool-only loop
		// guard indefinitely while spending model rounds on the same source.
		if batch.NoProgress && roundHadNarrative && roundSignature != "" {
			if roundSignature == priorToolRoundSignature {
				consecutiveIdenticalToolRounds = priorIdenticalToolRounds + 1
			} else {
				consecutiveIdenticalToolRounds = 1
			}
			lastToolCallRoundSignature = roundSignature
			if request.MaxConsecutiveIdenticalToolRounds > 0 &&
				consecutiveIdenticalToolRounds >= request.MaxConsecutiveIdenticalToolRounds {
				return interruptedRun(messages, &ToolRoundNoProgressError{
					Limit: request.MaxConsecutiveIdenticalToolRounds,
					Calls: appendNoProgressRecoveryCalls(noProgressRecoveryCalls, message.ToolCalls),
				})
			}
		}
		if batch.NoProgress {
			consecutiveNoProgressRounds++
			noProgressRecoveryCalls = appendNoProgressRecoveryCalls(noProgressRecoveryCalls, message.ToolCalls)
		} else {
			consecutiveNoProgressRounds = 0
			noProgressRecoveryCalls = noProgressRecoveryCalls[:0]
		}
		modelMessages = append([]Message(nil), messages...)
		if batch.NoProgress && noProgressReceiptsAreSuccessful(batch.Messages) {
			modelMessages = append(modelMessages, Message{
				Role: "system", Content: idempotentToolRoundRecoveryInstruction(message.ToolCalls),
			})
		}
		mediaBytesUsed = batch.MediaBytesUsed
		if toolErr != nil {
			return interruptedRun(messages, toolErr)
		}
		if batch.Terminal {
			if err := e.emit(Event{Type: EventFinal, Message: message.Content}); err != nil {
				return RunResult{FinalMessage: message, Messages: messages}, err
			}
			return RunResult{FinalMessage: message, Messages: messages}, nil
		}
		if request.MaxConsecutiveIdenticalToolRounds > 0 &&
			consecutiveNoProgressRounds >= request.MaxConsecutiveIdenticalToolRounds {
			return interruptedRun(messages, &ToolRoundNoProgressError{
				Limit: request.MaxConsecutiveIdenticalToolRounds,
				Calls: append([]ToolCall(nil), noProgressRecoveryCalls...),
			})
		}
		activeTools = expandEngineToolSchemas(e.Tools, toolSchemaExpansion, hasToolSchemaExpansion, activeTools)
		toolRounds++
	}
}

// interruptedRun keeps completed conversation state when a later model or
// control-plane failure ends the attempt. Callers such as plan-mode repair
// and in-process recovery must see the same tool receipts the durable
// journal already recorded, not an empty result.
func interruptedRun(messages []Message, err error) (RunResult, error) {
	return RunResult{Messages: messages}, err
}
