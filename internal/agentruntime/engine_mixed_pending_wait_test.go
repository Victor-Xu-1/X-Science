package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func mixedPendingResponses(readOnly bool) []ModelResponse {
	responses := []ModelResponse{}
	for index := 0; index < 4; index++ {
		calls := []ToolCall{}
		if !readOnly {
			calls = append(calls, ToolCall{ID: fmt.Sprintf("owned-wait-%d", index), Name: "await_owned", Arguments: json.RawMessage(`{}`)})
		}
		calls = append(calls, ToolCall{ID: fmt.Sprintf("observation-%d", index), Name: "observe_state", Arguments: json.RawMessage(`{}`)})
		responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: calls}})
	}
	return append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "Bounded mixed observations completed."}})
}

func TestMixedPendingWaitRequiresEveryActualHostClass(t *testing.T) {
	for _, test := range []struct {
		name              string
		value             map[string]any
		proof, onlyRead   bool
		allowed, repeated bool
	}{
		{name: "trusted read", value: map[string]any{"ok": true, "reused": true}, proof: true, allowed: true},
		{name: "bounded successful window", value: map[string]any{"ok": true, "truncated": true, "next_offset": 2, "content": "bounded"}, proof: true, allowed: true},
		{name: "JSON and MCP hints", value: map[string]any{"ok": true, "reused": true, "ReadOnlyObservation": true, "read_only": true, "annotations": map[string]any{"readOnlyHint": true}}},
		{name: "failed read", value: map[string]any{"ok": false, "error": "read failed"}, proof: true},
		{name: "partial unavailable read", value: map[string]any{"ok": true, "status": "partial"}, proof: true},
		{name: "not executed", value: map[string]any{"ok": true, "executed": false, "reused": true}, proof: true},
		{name: "unproved side-effecting companion", value: map[string]any{"ok": true, "effect": ToolEffectValue(ToolEffectUnchanged, "workspace")}},
		{name: "observation without pending work", value: map[string]any{"ok": true, "reused": true}, proof: true, onlyRead: true},
		{name: "source repetition remains independent", value: map[string]any{"ok": true, "reused": true}, proof: true, repeated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			gateway := FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
				calls++
				if call.Name == "await_owned" {
					return ToolResult{PendingWorkWait: true, Value: map[string]any{"ok": true, "status": "timeout"}}, nil
				}
				result := ToolResult{ReadOnlyObservation: test.proof, Value: test.value}
				if test.repeated {
					result.SourceObservation = &SourceObservation{State: SourceObservationRepeated, RepeatedOperations: 3}
				}
				return result, nil
			})
			result, err := (Engine{Model: &staticModelClient{responses: mixedPendingResponses(test.onlyRead)}, Tools: gateway}).Run(context.Background(), RunRequest{
				Messages: []Message{{Role: "user", Content: "Observe owned pending work."}}, Tools: []ToolSchema{{Name: "await_owned"}, {Name: "observe_state"}},
				MaxToolRounds: 8, MaxConsecutiveIdenticalToolRounds: 3,
			})
			if test.allowed {
				if err != nil || result.FinalMessage.Content == "" || calls != 8 {
					t.Fatalf("proved mixed wait was interrupted: calls=%d err=%v", calls, err)
				}
			} else {
				var stopped *ToolRoundNoProgressError
				if !errors.As(err, &stopped) || stopped.SourceRepetition != test.repeated || result.FinalMessage.Content != "" {
					t.Fatalf("unproved/mixed failure escaped its original guard: calls=%d err=%v", calls, err)
				}
			}
		})
	}
}

func TestMixedPendingWaitRetainsSegmentCancellationAndHiddenProof(t *testing.T) {
	var fromJSON ToolResult
	if err := json.Unmarshal([]byte(`{"Value":{"ok":true},"ReadOnlyObservation":true,"PendingWorkWait":true}`), &fromJSON); err != nil || fromJSON.ReadOnlyObservation || fromJSON.PendingWorkWait {
		t.Fatalf("JSON granted host-only authority: %+v err=%v", fromJSON, err)
	}
	for _, cancelAtRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		gateway := FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
			calls++
			if call.Name == "await_owned" {
				return ToolResult{PendingWorkWait: true, Value: map[string]any{"ok": true, "status": "timeout"}}, nil
			}
			if cancelAtRead {
				cancel()
			}
			return ToolResult{ReadOnlyObservation: true, Value: map[string]any{"ok": true, "reused": true}}, nil
		})
		_, err := (Engine{Model: &staticModelClient{responses: mixedPendingResponses(false)}, Tools: gateway}).Run(ctx, RunRequest{
			Messages: []Message{{Role: "user", Content: "Observe within the segment."}}, Tools: []ToolSchema{{Name: "await_owned"}, {Name: "observe_state"}},
			MaxToolRounds: 2, MaxConsecutiveIdenticalToolRounds: 3,
		})
		cancel()
		var segment *ToolRoundLimitError
		if cancelAtRead {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("mixed witness hid cancellation: %v", err)
			}
		} else if !errors.As(err, &segment) || calls != 4 {
			t.Fatalf("mixed witness escaped segment cap: calls=%d err=%v", calls, err)
		}
	}
}

func TestMixedPendingWaitAdmissionFeedbackUsesSameAllPredicate(t *testing.T) {
	calls := []ToolCall{{ID: "wait", Name: "await_owned"}, {ID: "read", Name: "observe_state"}, {ID: "rejected", Name: "unknown"}}
	engine := Engine{Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
		if call.Name == "await_owned" {
			return ToolResult{PendingWorkWait: true, Value: map[string]any{"ok": true}}, nil
		}
		return ToolResult{ReadOnlyObservation: true, Value: map[string]any{"ok": true}}, nil
	})}
	for _, rejected := range []bool{false, true} {
		selected := calls[:2]
		rejections := map[int]toolCallRejection{}
		if rejected {
			selected = calls
			rejections[2] = toolCallRejection{Code: "unadvertised_tool", Message: "This call is not admitted."}
		}
		result, err := engine.executeToolCallRoundWithRejections(context.Background(), selected, rejections, MediaPolicy{}, 0)
		if err != nil || result.OnlyPendingWaits || result.PendingWaitWithObservations == rejected || result.NextOrdinal != len(selected) {
			t.Fatalf("admission feedback drifted from the same ALL classifier: rejected=%t result=%+v err=%v", rejected, result, err)
		}
	}
}
