package agentruntime

import (
	"encoding/json"
	"errors"
	"strings"
)

// PreExecutionRejectionMessage restores a runtime-owned admission decision,
// never a tool execution receipt. Legacy checkpoints retained only the decision;
// their missing diagnostic must not be guessed or cause the proposal to run.
func PreExecutionRejectionMessage(call ToolCall) (Message, error) {
	if !call.RejectedBeforeExecution || strings.TrimSpace(call.ID) == "" {
		return Message{}, errors.New("canonical pre-execution rejection is required")
	}
	if len(call.PreExecutionRejection) == 0 {
		return modelVisibleToolRejectionMessage(call, toolCallRejection{
			Code:      "tool_call_preflight_rejected",
			Message:   "This proposal was rejected before execution by the runtime contract.",
			Recovery:  "The original diagnostic was not retained. Inspect the current tool contract and submit a corrected new call; this call was not executed.",
			Retryable: true,
			Preflight: true,
		})
	}
	var payload struct {
		OK       *bool  `json:"ok"`
		Executed *bool  `json:"executed"`
		Code     string `json:"code"`
		Message  string `json:"message"`
	}
	if len(call.PreExecutionRejection) > 16<<10 ||
		json.Unmarshal(call.PreExecutionRejection, &payload) != nil ||
		payload.OK == nil || *payload.OK || payload.Executed == nil || *payload.Executed ||
		strings.TrimSpace(payload.Code) == "" || strings.TrimSpace(payload.Message) == "" {
		return Message{}, errors.New("canonical pre-execution rejection feedback is invalid")
	}
	return Message{
		Role: "tool", ToolCallID: call.ID, Content: string(call.PreExecutionRejection), noProgress: true,
	}, nil
}
