package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const argumentRecoveryDuplicate = `{"value":"FIRST_PRIVATE_ARGUMENT","value":"SECOND_PRIVATE_ARGUMENT"}`

func TestEngineRecoversDuplicateToolArgumentsBeforeDefaults(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		t.Run(fmt.Sprintf("with_default_%t", withDefault), func(t *testing.T) {
			model := &capturingRequestModelClient{responses: []ModelResponse{
				argumentRecoveryResponse("duplicate", argumentRecoveryDuplicate),
				argumentRecoveryResponse("corrected", `{"value":"corrected"}`),
				{Message: Message{Role: "assistant", Content: "Corrected input completed."}},
			}}
			var executed []ToolCall
			var events []Event
			engine := Engine{
				Model: model,
				Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
					executed = append(executed, call)
					return ToolResult{Value: map[string]any{"ok": true}}, nil
				}),
				OnEvent: func(event Event) { events = append(events, event) },
			}
			result, err := engine.Run(context.Background(), RunRequest{
				Messages:      []Message{{Role: "user", Content: "Inspect the synthetic input."}},
				Tools:         []ToolSchema{argumentRecoverySchema(withDefault)},
				MaxToolRounds: 2,
			})
			if err != nil || result.FinalMessage.Content != "Corrected input completed." {
				t.Fatalf("corrected call did not finish: error=%v final=%q", err, result.FinalMessage.Content)
			}
			if len(executed) != 1 || executed[0].ID != "corrected" {
				t.Fatalf("invalid proposal reached execution: execution_count=%d", len(executed))
			}
			if len(model.requests) != 3 {
				t.Fatalf("model request count=%d, want 3", len(model.requests))
			}
			assertArgumentRecoveryFeedback(t, model.requests[1].Messages, "duplicate", true)
			var arguments map[string]any
			if json.Unmarshal(executed[0].Arguments, &arguments) != nil || arguments["value"] != "corrected" {
				t.Fatal("the corrected proposal lost its argument identity")
			}
			if withDefault && arguments["mode"] != "safe" {
				t.Fatal("valid corrected arguments did not receive their declared default")
			}
			if string(model.responses[0].Message.ToolCalls[0].Arguments) != argumentRecoveryDuplicate {
				t.Fatal("preparation rewrote the model's original argument bytes")
			}
			for _, event := range events {
				if event.ToolCallID == "duplicate" && (event.Type == EventToolStarted || event.Type == EventToolFailed) {
					t.Fatal("private structural repair emitted an execution lifecycle")
				}
			}
		})
	}
}

func TestEngineKeepsValidSiblingsWhenToolArgumentsNeedRepair(t *testing.T) {
	model := &capturingRequestModelClient{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "invalid-sibling", Name: "inspect_fixture", Arguments: json.RawMessage(argumentRecoveryDuplicate)},
			{ID: "valid-sibling", Name: "inspect_fixture", Arguments: json.RawMessage(`{"value":"valid"}`)},
		}}},
		argumentRecoveryResponse("corrected-sibling", `{"value":"corrected"}`),
		{Message: Message{Role: "assistant", Content: "Both valid inputs completed."}},
	}}
	var executed []string
	var checkpoints [][]ToolCall
	var events []Event
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
			executed = append(executed, call.ID)
			return ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
		OnEvent: func(event Event) {
			events = append(events, event)
			if event.Type == EventModelResponse && len(event.ToolCalls) > 0 {
				checkpoints = append(checkpoints, append([]ToolCall(nil), event.ToolCalls...))
			}
		},
	}
	result, err := engine.Run(context.Background(), RunRequest{
		Messages:      []Message{{Role: "user", Content: "Inspect the independent synthetic inputs."}},
		Tools:         []ToolSchema{argumentRecoverySchema(true)},
		MaxToolRounds: 2,
	})
	if err != nil || result.FinalMessage.Content != "Both valid inputs completed." {
		t.Fatalf("mixed batch did not continue: error=%v final=%q", err, result.FinalMessage.Content)
	}
	if len(executed) != 2 || executed[0] != "valid-sibling" || executed[1] != "corrected-sibling" {
		t.Fatalf("valid siblings were repeated, dropped, or joined by an invalid call: executions=%v", executed)
	}
	if len(model.requests) != 3 || len(checkpoints) != 2 || len(checkpoints[0]) != 2 {
		t.Fatalf("mixed batch boundaries changed: requests=%d checkpoints=%d", len(model.requests), len(checkpoints))
	}
	assertArgumentRecoveryFeedback(t, model.requests[1].Messages, "invalid-sibling", true)
	rejected := checkpoints[0][0]
	if !rejected.RejectedBeforeExecution || checkpoints[0][1].RejectedBeforeExecution {
		t.Fatal("the model-response boundary did not retain each sibling's admission decision")
	}
	var arguments map[string]any
	if json.Unmarshal(rejected.Arguments, &arguments) != nil || arguments == nil {
		t.Fatal("the rejected checkpoint does not contain a replayable JSON object")
	}
	feedback, err := PreExecutionRejectionMessage(rejected)
	if err != nil {
		t.Fatalf("the rejected checkpoint lost its runtime-owned feedback: %v", err)
	}
	assertArgumentRecoveryFeedback(t, []Message{feedback}, "invalid-sibling", true)
	if _, err := engine.ExecuteToolBatch(context.Background(), []ToolCall{rejected}, 0, MediaPolicy{}, 0); err != nil {
		t.Fatalf("replaying the rejected call failed: %v", err)
	}
	if len(executed) != 2 {
		t.Fatal("replaying a rejected call invoked the gateway")
	}
	for _, event := range events {
		if event.ToolCallID == "invalid-sibling" && event.Type == EventToolStarted {
			t.Fatal("the rejected sibling published an execution start")
		}
	}
}

func TestEngineBoundsRepeatedMalformedToolArguments(t *testing.T) {
	model := &capturingRequestModelClient{}
	for index := 1; index <= maxPrivatePreflightRepairAttempts+6; index++ {
		model.responses = append(model.responses, argumentRecoveryResponse(fmt.Sprintf("invalid-%d", index), argumentRecoveryDuplicate))
	}
	var executions int
	var checkpoints [][]ToolCall
	var starts int
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			executions++
			return ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
		OnEvent: func(event Event) {
			if event.Type == EventModelResponse && len(event.ToolCalls) > 0 {
				checkpoints = append(checkpoints, append([]ToolCall(nil), event.ToolCalls...))
			}
			if event.Type == EventToolStarted {
				starts++
			}
		},
	}
	_, err := engine.Run(context.Background(), RunRequest{
		Messages:                          []Message{{Role: "user", Content: "Inspect the synthetic input."}},
		Tools:                             []ToolSchema{argumentRecoverySchema(true)},
		MaxToolRounds:                     8,
		MaxConsecutiveIdenticalToolRounds: 2,
	})
	var noProgress *ToolRoundNoProgressError
	if !errors.As(err, &noProgress) {
		t.Fatalf("repeated invalid proposals did not reach the existing bounded interruption: %v", err)
	}
	if executions != 0 || starts != 0 {
		t.Fatalf("an exhausted invalid family reached execution: executions=%d starts=%d", executions, starts)
	}
	wantRequests := maxPrivatePreflightRepairAttempts + 2
	if len(model.requests) != wantRequests {
		t.Fatalf("repair exceeded or bypassed the existing window: requests=%d want=%d", len(model.requests), wantRequests)
	}
	exhaustedID := fmt.Sprintf("invalid-%d", maxPrivatePreflightRepairAttempts+1)
	assertArgumentRecoveryFeedback(t, model.requests[wantRequests-1].Messages, exhaustedID, false)
	if len(checkpoints) != 1 || len(checkpoints[0]) != 1 || !checkpoints[0][0].RejectedBeforeExecution {
		t.Fatal("the exhausted private repair window did not leave one bounded rejection checkpoint")
	}
	feedback, err := PreExecutionRejectionMessage(checkpoints[0][0])
	if err != nil {
		t.Fatalf("the exhausted rejection is not replayable: %v", err)
	}
	assertArgumentRecoveryFeedback(t, []Message{feedback}, exhaustedID, false)
}

func argumentRecoveryResponse(id, arguments string) ModelResponse {
	return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID: id, Name: "inspect_fixture", Arguments: json.RawMessage(arguments),
	}}}}
}

func argumentRecoverySchema(withDefault bool) ToolSchema {
	properties := map[string]any{"value": map[string]any{"type": "string"}}
	if withDefault {
		properties["mode"] = map[string]any{"type": "string", "default": "safe"}
	}
	return ToolSchema{Name: "inspect_fixture", Parameters: map[string]any{
		"type": "object", "properties": properties, "required": []string{"value"},
	}}
}

func assertArgumentRecoveryFeedback(t *testing.T, messages []Message, callID string, retryable bool) {
	t.Helper()
	for _, message := range messages {
		if message.Role != "tool" || message.ToolCallID != callID {
			continue
		}
		var feedback struct {
			OK        *bool  `json:"ok"`
			Executed  *bool  `json:"executed"`
			Retryable *bool  `json:"retryable"`
			Code      string `json:"code"`
		}
		if json.Unmarshal([]byte(message.Content), &feedback) != nil || feedback.OK == nil || *feedback.OK ||
			feedback.Executed == nil || *feedback.Executed || feedback.Code != "invalid_tool_arguments" ||
			feedback.Retryable == nil || *feedback.Retryable != retryable {
			t.Fatalf("call %q lacks the expected nonexecuted argument-rejection feedback", callID)
		}
		if strings.Contains(message.Content, "FIRST_PRIVATE_ARGUMENT") || strings.Contains(message.Content, "SECOND_PRIVATE_ARGUMENT") {
			t.Fatal("structural feedback exposed raw argument values")
		}
		return
	}
	t.Fatalf("call %q has no model-visible rejection feedback", callID)
}
