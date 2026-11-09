package agentruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"x-science/internal/toolcontract"
)

type toolArgumentsOrigin struct {
	digest [sha256.Size]byte
	bytes  int
}

// OriginalArgumentsSource returns immutable provenance values, not a grant.
// Only the original strict host diagnostic can attach this source identity.
func (call ToolCall) OriginalArgumentsSource() (string, int, bool) {
	if call.originalArguments == nil {
		return "", 0, false
	}
	return hex.EncodeToString(call.originalArguments.digest[:]), call.originalArguments.bytes, true
}

// Keep invalid proposals on the existing bounded protocol-repair path. The
// inert object is replayable JSON, never permission to execute the call.
// This runs at the completed-response boundary, before presentation wrappers
// can strip legacy fields and before schema defaults decode argument maps.
func prepareModelToolArguments(calls []ToolCall) ([]ToolCall, error) {
	var prepared []ToolCall
	for index, call := range calls {
		if call.originalArguments != nil && !bytes.Equal(call.Arguments, []byte(`{}`)) {
			return nil, errors.New("tool argument source identity conflicts with its inert proposal")
		}
		if len(bytes.TrimSpace(call.Arguments)) == 0 || toolcontract.ValidateArgumentsObject(call.Arguments) == nil {
			continue
		}
		if prepared == nil {
			prepared = append([]ToolCall(nil), calls...)
		}
		prepared[index].Arguments = json.RawMessage(`{}`)
		prepared[index].originalArguments = &toolArgumentsOrigin{digest: sha256.Sum256(call.Arguments), bytes: len(call.Arguments)}
		if prepared[index].ProviderProtocolDiagnostic == "" {
			prepared[index].ProviderProtocolDiagnostic = "Tool arguments require one valid JSON object with unique keys. Correct the original proposal before execution."
		}
	}
	if prepared != nil {
		return prepared, nil
	}
	return calls, nil
}
