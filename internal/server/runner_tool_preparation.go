package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"x-science/internal/agentruntime"
	"x-science/internal/toolcontract"
)

func checkpointOriginalArguments(call agentruntime.ToolCall, arguments json.RawMessage) (string, int, bool, error) {
	if digest, size, found := call.OriginalArgumentsSource(); found {
		if !call.RejectedBeforeExecution || !bytes.Equal(arguments, []byte(`{}`)) || !bytes.Equal(call.Arguments, []byte(`{}`)) {
			return "", 0, false, classifySessionRunnerPreparationFailure("tool_rejection", errors.New("original tool argument identity conflicts with its rejection"))
		}
		return digest, size, true, nil
	}
	if bytes.Equal(arguments, call.Arguments) {
		return "", 0, false, nil
	}
	digest := sha256.Sum256(call.Arguments)
	return hex.EncodeToString(digest[:]), len(call.Arguments), true, nil
}

// Only a runtime-owned rejection may replace an invalid proposal with an inert
// object. Its original digest/length and exact feedback stay on the canonical
// checkpoint; replay must retain the rejection rather than admit this object.
func rejectedCheckpointArguments(call agentruntime.ToolCall) (json.RawMessage, error) {
	if !call.RejectedBeforeExecution {
		return call.Arguments, nil
	}
	if _, err := agentruntime.PreExecutionRejectionMessage(call); err != nil {
		return nil, classifySessionRunnerPreparationFailure("tool_rejection", err)
	}
	if toolcontract.ValidateArgumentsObject(call.Arguments) != nil {
		return json.RawMessage(`{}`), nil
	}
	return call.Arguments, nil
}
