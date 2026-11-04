package server

import (
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestAgentRuntimeToolNumericWireContract(t *testing.T) {
	var ordinary map[string]any
	if err := decodeAgentRuntimeToolArguments("ordinary", []byte(`{"value":1.25}`), &ordinary); err != nil || ordinary["value"] != float64(1.25) {
		t.Fatalf("existing numeric consumer changed: %#v %v", ordinary, err)
	}
	for _, raw := range []string{`{"value":1} {}`, `{"value":NaN}`, `{"value":01}`} {
		var value map[string]any
		if decodeAgentRuntimeToolArguments(librarySaveToInboxToolName, []byte(raw), &value) == nil {
			t.Fatalf("invalid arguments accepted: %s", raw)
		}
	}
	for _, number := range []json.Number{"", "NaN", "01", "Infinity"} {
		if _, _, err := normalizeKernelHostArgumentsForMethod("host.library.save_to_inbox", []any{number}, nil); err == nil {
			t.Fatalf("invalid Host numeric token accepted: %q", number)
		}
	}
	deep := any(json.Number("1e400"))
	for i := 0; i < maxKernelHostJSONDepth+2; i++ {
		deep = []any{deep}
	}
	if _, _, err := normalizeKernelHostArgumentsForMethod("host.library.save_to_inbox", []any{deep}, nil); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("lossless codec bypassed Host depth bound: %v", err)
	}
	// The generic execution identity must distinguish a changed metadata value
	// even when the runtime's failed-call guard compares normalized arguments.
	gateway := serverAgentRuntimeToolGateway{}
	first, ok := gateway.ToolCallExecutionIdentity(agentruntime.ToolCall{Name: librarySaveToInboxToolName, Arguments: json.RawMessage(`{"metadata":{"value":9007199254740992}}`)})
	second, otherOK := gateway.ToolCallExecutionIdentity(agentruntime.ToolCall{Name: librarySaveToInboxToolName, Arguments: json.RawMessage(`{"metadata":{"value":9007199254740993}}`)})
	if !ok || !otherOK || string(first.Arguments) == string(second.Arguments) {
		t.Fatalf("distinct lossless execution identities collapsed: %s %s", first.Arguments, second.Arguments)
	}
}
