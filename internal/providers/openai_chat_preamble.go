package providers

import (
	"crypto/sha256"
	"fmt"

	"x-science/internal/agentruntime"
)

func openAIChatPreambleToolNames(advertised [][]agentruntime.ToolSchema) map[string]bool {
	names := make(map[string]bool)
	if len(advertised) == 1 {
		for _, tool := range advertised[0] {
			names[tool.Name] = true
		}
	}
	return names
}

// This is positive authority for only the already received assistant prefix,
// never for function execution or the remainder of the response. Explicit role,
// message and function metadata distinguish it from the any-fragment marker.
// A later body or function-name fragment keeps its original decoder semantics;
// it cannot amend this frozen prefix and is not a new protocol error.
func (a *openAIChatStreamAccumulator) nativePreambleBoundary(advertised map[string]bool) (agentruntime.ModelStreamEvent, bool) {
	call := a.toolCalls[0]
	if a.nativePreambleSent || a.nativeBodyUnbound || !a.roleBound || a.role != "assistant" ||
		a.requestID == "" || a.content.Len() == 0 || call == nil || !call.FunctionTypeBound ||
		call.ID == "" || !advertised[call.Name] {
		return agentruntime.ModelStreamEvent{}, false
	}
	a.nativePreambleSent = true
	identity := sha256.Sum256([]byte(a.requestID + "\x00" + call.ID))
	return agentruntime.ModelStreamEvent{
		Kind: agentruntime.ModelStreamEventNativePreambleBoundary, BlockID: fmt.Sprintf("native-%x", identity),
	}, true
}
