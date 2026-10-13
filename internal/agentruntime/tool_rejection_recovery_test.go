package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type rejectedSuccessorGateway struct {
	calls    int
	approved bool
}

func (g *rejectedSuccessorGateway) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	if call.ID != "first" {
		return ToolResult{}, errors.New("rejected call reached execution")
	}
	if !g.approved {
		return ToolResult{}, &PauseError{Status: "awaiting_approval"}
	}
	g.calls++
	return ToolResult{Value: map[string]any{"ok": true}}, nil
}

func (*rejectedSuccessorGateway) ToolCallPreflightDiagnostics(_ context.Context, calls []ToolCall) (map[int]string, error) {
	result := map[int]string{}
	for index, call := range calls {
		if call.ID == "second" {
			result[index] = `{"code":"runtime_preflight_required","message":"A supported route is required.","recovery":"Choose the current supported route."}`
		}
	}
	return result, nil
}

func TestEngineRestoresRejectedSuccessorAfterApprovalPause(t *testing.T) {
	gateway := &rejectedSuccessorGateway{}
	var checkpoint []ToolCall
	var events []Event
	engine := Engine{
		Model: &staticModelClient{responses: []ModelResponse{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "first", Name: "repl", Arguments: json.RawMessage(`{"code":"print(1)","human_description":"First call"}`)},
			{ID: "second", Name: "repl", Arguments: json.RawMessage(`{"code":"print(2)","human_description":"Second call"}`)},
		}}}}},
		Tools: gateway,
		OnEvent: func(event Event) {
			events = append(events, event)
			if event.Type == EventModelResponse {
				checkpoint = event.ToolCalls
			}
		},
	}
	_, err := engine.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: "user", Content: "Run the batch"}},
		Tools:    []ToolSchema{{Name: "repl"}},
	})
	var pause *PauseError
	if !errors.As(err, &pause) || gateway.calls != 0 || len(checkpoint) != 2 {
		t.Fatalf("pause=%v executions=%d checkpoint=%#v", err, gateway.calls, checkpoint)
	}
	if checkpoint[0].RejectedBeforeExecution || !checkpoint[1].RejectedBeforeExecution ||
		!strings.Contains(string(checkpoint[1].PreExecutionRejection), "runtime_preflight_required") {
		t.Fatalf("checkpoint lost exact rejection: %#v", checkpoint)
	}
	gateway.approved = true
	resumed, err := engine.ExecuteToolBatch(context.Background(), checkpoint, 0, MediaPolicy{}, 0)
	if err != nil || resumed.NextOrdinal != 2 || gateway.calls != 1 || len(resumed.Messages) != 2 {
		t.Fatalf("resumed=%#v err=%v executions=%d", resumed, err, gateway.calls)
	}
	for _, event := range events {
		if event.Type == EventToolStarted && event.ToolCallID == "second" {
			t.Fatal("rejected successor published an execution start")
		}
	}
	var result map[string]any
	if json.Unmarshal([]byte(resumed.Messages[1].Content), &result) != nil ||
		result["executed"] != false || result["code"] != "runtime_preflight_required" {
		t.Fatalf("rejection feedback=%s", resumed.Messages[1].Content)
	}
}

func TestEngineRestoredRejectionCannotExecuteOnLegacyInvalidOrCancelledFeedback(t *testing.T) {
	for _, kind := range []string{"legacy", "invalid", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			call := ToolCall{ID: "rejected", Name: "repl", Arguments: json.RawMessage(`{}`), RejectedBeforeExecution: true}
			ctx := context.Background()
			if kind == "invalid" {
				call.PreExecutionRejection = json.RawMessage(`{"ok":true,"executed":true}`)
			}
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var events []Event
			engine := Engine{
				Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
					t.Fatal("rejected call executed")
					return ToolResult{}, nil
				}),
				OnEvent: func(event Event) { events = append(events, event) },
			}
			result, err := engine.ExecuteToolBatch(ctx, []ToolCall{call}, 0, MediaPolicy{}, 0)
			if kind == "legacy" {
				if err != nil || result.NextOrdinal != 1 || !result.NoProgress ||
					len(events) != 1 || !events[0].RejectedBeforeExecution ||
					!strings.Contains(result.Messages[0].Content, "original diagnostic was not retained") {
					t.Fatalf("legacy recovery=%#v events=%#v err=%v", result, events, err)
				}
			} else if err == nil || len(events) != 0 {
				t.Fatalf("invalid/cancelled recovery emitted events=%#v err=%v", events, err)
			}
		})
	}
	var providerCall ToolCall
	if err := json.Unmarshal([]byte(`{"id":"provider","name":"repl","rejectedBeforeExecution":true,"preExecutionRejection":{"ok":false,"executed":false}}`), &providerCall); err != nil {
		t.Fatal(err)
	}
	if providerCall.RejectedBeforeExecution || len(providerCall.PreExecutionRejection) != 0 {
		t.Fatal("provider JSON forged runtime-owned admission")
	}
}
