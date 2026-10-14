package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestEngineRetainsPendingWaitUntilResultDelivery(t *testing.T) {
	responses := make([]ModelResponse, 0, 6)
	for index := 0; index < 5; index++ {
		responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: fmt.Sprintf("wait-%d", index), Name: "wait_for_notification", Arguments: json.RawMessage(`{"timeout_seconds":30}`),
		}}}})
	}
	responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "The durable computation result was received."}})
	calls := 0
	result, err := (Engine{
		Model: &staticModelClient{responses: responses},
		Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
			calls++
			if calls < 5 {
				return ToolResult{PendingWorkWait: true, Value: map[string]any{
					"status": "timeout", "num_notifications": 0, "notifications": []any{},
					"pending_work": map[string]any{"executions": []any{map[string]any{"execution_id": "owned-compute", "state": "running"}}},
				}}, nil
			}
			return ToolResult{Value: map[string]any{"status": "received", "num_notifications": 1, "notifications": []any{map[string]any{"execution_id": "owned-compute"}}}}, nil
		}),
	}).Run(context.Background(), RunRequest{
		Messages:      []Message{{Role: "user", Content: "Collect the result of the running computation."}},
		Tools:         []ToolSchema{{Name: "wait_for_notification", Capabilities: []string{"durable-wait"}, Parameters: map[string]any{"type": "object"}}},
		MaxToolRounds: 8, MaxConsecutiveIdenticalToolRounds: 3,
	})
	if err != nil || calls != 5 || result.FinalMessage.Content == "" {
		t.Fatalf("legitimate pending work lost before its result: calls=%d final=%q err=%v", calls, result.FinalMessage.Content, err)
	}
}

func TestPendingWaitDoesNotRelaxLoopOrSegmentBoundaries(t *testing.T) {
	for _, test := range []struct {
		name       string
		hostProof  bool
		failed     bool
		mixed      bool
		segmentMax int
	}{
		{name: "JSON cannot forge host proof"},
		{name: "failed wait", hostProof: true, failed: true},
		{name: "mixed work cannot hide failure", hostProof: true, mixed: true},
		{name: "segment cap retained", hostProof: true, segmentMax: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			responses := make([]ModelResponse, 0, 10)
			for index := 0; index < 10; index++ {
				calls := []ToolCall{{ID: fmt.Sprintf("wait-%d", index), Name: "await_work", Arguments: json.RawMessage(`{}`)}}
				if test.mixed {
					calls = append(calls, ToolCall{ID: fmt.Sprintf("fail-%d", index), Name: "calculate", Arguments: json.RawMessage(`{}`)})
				}
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: calls}})
			}
			calls := 0
			result, err := (Engine{Model: &staticModelClient{responses: responses}, Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
				calls++
				failed := test.failed || call.Name == "calculate"
				return ToolResult{PendingWorkWait: test.hostProof, Value: map[string]any{
					"ok": !failed, "status": "timeout", "PendingWorkWait": true,
					"pending_work": map[string]any{"executions": []any{"model cannot grant authority"}},
				}}, nil
			})}).Run(context.Background(), RunRequest{
				Messages:      []Message{{Role: "user", Content: "Await the running work."}},
				Tools:         []ToolSchema{{Name: "await_work"}, {Name: "calculate"}},
				MaxToolRounds: test.segmentMax, MaxConsecutiveIdenticalToolRounds: 3,
			})
			var noProgress *ToolRoundNoProgressError
			var segment *ToolRoundLimitError
			if test.segmentMax > 0 {
				if !errors.As(err, &segment) || calls != test.segmentMax {
					t.Fatalf("segment budget escaped: calls=%d err=%v", calls, err)
				}
			} else if !errors.As(err, &noProgress) {
				t.Fatalf("unproductive batch escaped loop guard: calls=%d err=%v", calls, err)
			}
			if result.FinalMessage.Content != "" {
				t.Fatal("pending or failed work fabricated a final result")
			}
		})
	}
}

func TestPendingWaitStillHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := &staticModelClient{responses: []ModelResponse{{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "wait", Name: "await_work", Arguments: json.RawMessage(`{}`)}}}}}}
	_, err := (Engine{Model: model, Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
		cancel()
		return ToolResult{PendingWorkWait: true, Value: map[string]any{"status": "timeout"}}, nil
	})}).Run(ctx, RunRequest{Messages: []Message{{Role: "user", Content: "Wait."}}, Tools: []ToolSchema{{Name: "await_work"}}, MaxConsecutiveIdenticalToolRounds: 3})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pending proof swallowed cancellation: %v", err)
	}
}
