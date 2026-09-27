package agentruntime

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFailedCallIsNotReopenedBySameToolReadOnlyInspection(t *testing.T) {
	for _, inspection := range []string{"python engine.py --help", "wc -l result.csv", "cat failure.json"} {
		t.Run(inspection, func(t *testing.T) {
			executions := 0
			guard := newFailedToolCallGuard(FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
				var input map[string]any
				if err := json.Unmarshal(call.Arguments, &input); err != nil {
					t.Fatal(err)
				}
				if input["command"] == inspection {
					return ToolResult{Value: map[string]any{"ok": true, "stdout": "observed", "files_written": []any{}}}, nil
				}
				executions++
				return ToolResult{Value: map[string]any{
					"ok": false, "code": "bash_nonzero_exit", "stderr": "ValueError: no candidates",
					"files_written": []any{},
				}}, nil
			}))
			failed := ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"python engine.py --input sample.dat","environment":"analysis"}`)}
			if _, err := guard.Execute(context.Background(), failed); err != nil {
				t.Fatal(err)
			}
			arguments, _ := json.Marshal(map[string]any{"command": inspection, "environment": "analysis"})
			if _, err := guard.Execute(context.Background(), ToolCall{Name: "bash", Arguments: arguments}); err != nil {
				t.Fatal(err)
			}
			repeated, err := guard.Execute(context.Background(), failed)
			if err != nil {
				t.Fatal(err)
			}
			value := mapValueForTest(t, repeated.Value)
			if executions != 1 || value["code"] != "repeated_failed_tool_call" || value["executed"] != false {
				t.Fatalf("read-only inspection reopened failed execution: attempts=%d value=%#v", executions, value)
			}
		})
	}
}
