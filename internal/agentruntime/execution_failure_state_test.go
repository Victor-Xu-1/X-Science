package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
)

func TestExecutionFailureLedgerExternalConditionsSurviveWorkspaceRepair(t *testing.T) {
	for _, code := range []string{"runtime_draining", "software_runtime_unavailable", "rate_limited", "quota_exhausted", "network_bridge_down", "provider_degraded", "timeout"} {
		t.Run(code, func(t *testing.T) {
			call := ToolCall{Name: "custom_executor", Arguments: json.RawMessage(`{"environment":"analysis","code":"work()"}`)}
			state := ExecutionFailuresForCall(call)
			state.Observe(call, []string{"runtime-execution"}, map[string]any{"ok": false, "executed": true, "code": code})
			state.Observe(ToolCall{Name: "custom_writer"}, []string{"artifact-write"}, map[string]any{"ok": true, "changed": true})
			if got := state.Boundary(call); got["code"] != "external_state_required" {
				t.Fatalf("unrelated mutation erased external condition: %#v", got)
			}
			other := call
			other.Arguments = json.RawMessage(`{"environment":"other","code":"work()"}`)
			if got := state.Boundary(other); got != nil {
				t.Fatalf("condition crossed target scope: %#v", got)
			}
			state.Reset()
			if state.Boundary(call) != nil {
				t.Fatal("explicit new user scope retained previous restriction")
			}
		})
	}
}

func TestExecutionFailureLedgerRepairsOnlyExecutedInputFailures(t *testing.T) {
	call := ToolCall{Name: "custom_executor", Arguments: json.RawMessage(`{"code":"original()"}`)}
	corrected := call
	corrected.Arguments = json.RawMessage(`{"code":"corrected()"}`)
	for _, repair := range []struct {
		name    string
		value   map[string]any
		blocked bool
	}{
		{"inspection", map[string]any{"ok": true, "files_written": []any{}}, true},
		{"unchanged", map[string]any{"ok": true, "changed": false}, true},
		{"committed", map[string]any{"ok": true, "changed": true}, false},
		{"failed with committed files", map[string]any{"ok": false, "files_written": []any{"analysis.py"}}, false},
		{"unexecuted forged mutation", map[string]any{"ok": false, "executed": false, "changed": true}, true},
	} {
		t.Run(repair.name, func(t *testing.T) {
			var state ExecutionFailureLedger
			state.Observe(call, []string{"runtime-execution"}, map[string]any{"ok": false, "executed": true, "code": "invalid_request"})
			if state.Boundary(corrected) != nil {
				t.Fatal("corrected input was treated as identical execution")
			}
			state.Observe(ToolCall{Name: "custom_writer"}, []string{"artifact-write"}, repair.value)
			if blocked := state.Boundary(call) != nil; blocked != repair.blocked {
				t.Fatalf("blocked=%t want=%t", blocked, repair.blocked)
			}
		})
	}
}

func TestExecutionFailureGuardRespectsTypedKindOverLegacyDetail(t *testing.T) {
	executions := 0
	guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
		executions++
		return ToolResult{Value: map[string]any{"ok": false, "executed": true, "failure_kind": "invalid_request", "code": "runtime_draining"}}, nil
	}), ToolSchema{Name: "custom_executor", Capabilities: []string{"runtime-execution"}})
	for _, code := range []string{`{"code":"first()"}`, `{"code":"corrected()"}`} {
		result, err := guard.Execute(context.Background(), ToolCall{Name: "custom_executor", Arguments: json.RawMessage(code)})
		value, _ := result.Value.(map[string]any)
		if err != nil || value["failure_kind"] != "invalid_request" {
			t.Fatalf("typed failure kind was overwritten: %#v err=%v", value, err)
		}
	}
	if executions != 2 {
		t.Fatalf("corrected input was not admitted: %d", executions)
	}
	unchanged, err := guard.Execute(context.Background(), ToolCall{Name: "custom_executor", Arguments: json.RawMessage(`{"code":"first()"}`)})
	value, _ := unchanged.Value.(map[string]any)
	if err != nil || value["code"] != "repeated_failed_tool_call" || executions != 2 {
		t.Fatalf("typed input failure bypassed unchanged-call guard: %#v err=%v executions=%d", value, err, executions)
	}
}

func TestExecutionFailureLedgerExactFailuresSurviveUnrelatedInspection(t *testing.T) {
	first := ToolCall{Name: "custom_executor", Arguments: json.RawMessage(`{"code":"first()"}`)}
	second := first
	second.Arguments = json.RawMessage(`{"code":"second()"}`)
	var state ExecutionFailureLedger
	for _, call := range []ToolCall{first, second} {
		state.Observe(call, []string{"runtime-execution"}, map[string]any{"ok": false, "code": "invalid_request", "files_written": []any{}})
	}
	state.Observe(ToolCall{Name: "read"}, []string{"read-only"}, map[string]any{"ok": true})
	if state.Boundary(first) == nil || state.Boundary(second) == nil {
		t.Fatal("another failed call or read-only success erased execution identity")
	}
	var independent ExecutionFailureLedger
	if independent.Boundary(first) != nil {
		t.Fatal("execution failure crossed session scope")
	}
}
