package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
)

func TestLiveExternalFailureSurvivesUnrelatedMutation(t *testing.T) {
	ctx := context.Background()
	executions := 0
	guard := newFailedToolCallGuard(FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
		if call.Name == "edit_file" {
			return ToolResult{Value: map[string]any{"ok": true, "executed": true, "changed": true}}, nil
		}
		executions++
		return ToolResult{Value: map[string]any{
			"ok": false, "executed": true, "code": "runtime_draining", "error": "runtime draining",
		}}, nil
	}), ToolSchema{Name: "python", Capabilities: []string{"runtime-execution"}},
		ToolSchema{Name: "edit_file", Capabilities: []string{"artifact-write"}})
	call := ToolCall{ID: "first", Name: "python", Arguments: json.RawMessage(`{"environment":"analysis","code":"run_job()"}`)}
	if _, err := guard.Execute(ctx, call); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(ctx, ToolCall{ID: "edit", Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"notes.md","old_string":"old","new_string":"new"}`)}); err != nil {
		t.Fatal(err)
	}
	call.ID = "retry"
	result, err := guard.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok || value["code"] != "external_state_required" || value["executed"] != false || executions != 1 {
		t.Fatalf("unexpected live decision: result=%#v executions=%d", result.Value, executions)
	}
	t.Logf("live policy: code=%v; executor invocations=%d", value["code"], executions)
}
