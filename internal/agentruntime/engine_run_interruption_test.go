package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
)

type cancelAfterToolModel struct {
	calls atomic.Int64
}

func (m *cancelAfterToolModel) Complete(context.Context, ModelRequest) (ModelResponse, error) {
	if m.calls.Add(1) != 1 {
		return ModelResponse{}, errors.New("model request started after the run was cancelled")
	}
	return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID: "work-1", Name: "noop", Arguments: json.RawMessage(`{"step":1}`),
	}}}}, nil
}

type laterModelFailure struct {
	calls atomic.Int64
}

func (m *laterModelFailure) Complete(context.Context, ModelRequest) (ModelResponse, error) {
	if m.calls.Add(1) == 1 {
		return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "work-1", Name: "noop", Arguments: json.RawMessage(`{"step":1}`),
		}}}}, nil
	}
	return ModelResponse{}, errors.New("provider stream reset after the first tool round")
}

func TestEnginePreservesCompletedRoundsWhenCancelledBeforeNextModelRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &cancelAfterToolModel{}
	var events []Event
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			cancel()
			return ToolResult{Value: map[string]any{"ok": true, "step": 1}}, nil
		}),
		OnEvent: func(event Event) { events = append(events, event) },
	}
	result, err := engine.Run(ctx, RunRequest{
		Messages: []Message{{Role: "user", Content: "continue the long task"}},
		Tools:    []ToolSchema{{Name: "noop", Parameters: map[string]any{"type": "object"}}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("model calls=%d; cancellation must not start another provider request", model.calls.Load())
	}
	if !hasCompletedToolReceipt(result.Messages, "work-1") {
		t.Fatalf("cancelled run dropped completed tool state: %#v", result.Messages)
	}
	requestEvents := 0
	for _, event := range events {
		if event.Type == EventModelRequest {
			requestEvents++
		}
	}
	if requestEvents != 1 {
		t.Fatalf("EventModelRequest count=%d; cancelled run must not invent a second request", requestEvents)
	}
}

func TestEnginePreservesCompletedRoundsWhenLaterModelRoundFails(t *testing.T) {
	model := &laterModelFailure{}
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Value: map[string]any{"ok": true, "step": 1}}, nil
		}),
	}
	result, err := engine.Run(context.Background(), RunRequest{
		Messages: []Message{{Role: "user", Content: "continue the long task"}},
		Tools:    []ToolSchema{{Name: "noop", Parameters: map[string]any{"type": "object"}}},
	})
	if err == nil || err.Error() != "provider stream reset after the first tool round" {
		t.Fatalf("error=%v", err)
	}
	if !hasCompletedToolReceipt(result.Messages, "work-1") {
		t.Fatalf("failed later model round dropped completed tool state: %#v", result.Messages)
	}
}

func TestEnginePreservesCompletedRoundsWhenLaterFinalMessageIsEmpty(t *testing.T) {
	model := &staticModelClient{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "work-1", Name: "noop", Arguments: json.RawMessage(`{"step":1}`),
		}}}},
		{Message: Message{Role: "assistant", Content: " \n\t"}},
	}}
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
	}
	result, err := engine.Run(t.Context(), RunRequest{
		Messages: []Message{{Role: "user", Content: "continue"}},
		Tools:    []ToolSchema{{Name: "noop", Parameters: map[string]any{"type": "object"}}},
	})
	if err == nil || err.Error() != "agent runtime response message is empty" {
		t.Fatalf("error=%v", err)
	}
	if !hasCompletedToolReceipt(result.Messages, "work-1") {
		t.Fatal("empty later final message discarded completed tool receipts")
	}
	if result.FinalMessage.Role != "" {
		t.Fatal("empty failed final message was published as success")
	}
}

func TestEnginePreservesCompletedRoundsWhenToolRoundLimitStopsLaterProposal(t *testing.T) {
	model := &staticModelClient{responses: []ModelResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "work-1", Name: "noop", Arguments: json.RawMessage(`{"step":1}`),
		}}}},
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "work-2", Name: "noop", Arguments: json.RawMessage(`{"step":2}`),
		}}}},
	}}
	engine := Engine{
		Model: model,
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
	}
	result, err := engine.Run(context.Background(), RunRequest{
		Messages:      []Message{{Role: "user", Content: "loop"}},
		Tools:         []ToolSchema{{Name: "noop", Parameters: map[string]any{"type": "object"}}},
		MaxToolRounds: 1,
	})
	var limitErr *ToolRoundLimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != 1 {
		t.Fatalf("error=%v", err)
	}
	if !hasCompletedToolReceipt(result.Messages, "work-1") {
		t.Fatalf("tool-round limit dropped the completed first round: %#v", result.Messages)
	}
	for _, message := range result.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) == 1 && message.ToolCalls[0].ID == "work-2" {
			t.Fatalf("unpublished later proposal was treated as completed state: %#v", result.Messages)
		}
	}
}

func TestEnginePreservesCompletedRoundsWhenNarratedReadsStop(t *testing.T) {
	model := &repeatedReadModel{}
	gateway := &reusedReadGateway{}
	result, err := (Engine{Model: model, Tools: gateway}).Run(context.Background(), RunRequest{
		Messages:                          []Message{{Role: "user", Content: "review the source"}},
		Tools:                             []ToolSchema{{Name: "web_fetch"}},
		MaxConsecutiveIdenticalToolRounds: 3,
	})
	var noProgress *ToolRoundNoProgressError
	if !errors.As(err, &noProgress) || noProgress.Limit != 3 {
		t.Fatalf("error=%v", err)
	}
	if !hasCompletedToolReceipt(result.Messages, "read-1") || !hasCompletedToolReceipt(result.Messages, "read-3") {
		t.Fatalf("no-progress stop dropped executed read receipts: %#v", result.Messages)
	}
}

func hasCompletedToolReceipt(messages []Message, toolCallID string) bool {
	sawAssistant := false
	sawTool := false
	for _, message := range messages {
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				if call.ID == toolCallID {
					sawAssistant = true
				}
			}
		}
		if message.Role == "tool" && message.ToolCallID == toolCallID && message.Content != "" {
			sawTool = true
		}
	}
	return sawAssistant && sawTool
}
