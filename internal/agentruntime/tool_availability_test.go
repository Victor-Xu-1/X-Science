package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUnavailableEnvelopeIsNotSuccessfulProgress(t *testing.T) {
	for _, value := range []any{
		map[string]any{"status": "not_available"},
		map[string]any{"ok": true, "result": map[string]any{"status": "not_available", "available": false, "body": ""}},
		map[string]any{"ok": true, "result": testToolResultEnvelope{result: map[string]any{"status": "not_available"}}},
		map[string]any{"result": testToolResultEnvelope{result: map[string]any{"sourceUnavailable": true}}},
		map[string]any{"sources": []any{map[string]any{"status": "not_available"}}},
	} {
		if got := ClassifyToolResult(value); got != ToolResultUnavailable || got.HardFailed() {
			t.Errorf("unavailable source classified as %s: %#v", got, value)
		}
		if !toolResultReportsNoProgress(value) {
			t.Errorf("unavailable source refreshed progress: %#v", value)
		}
	}
	for _, value := range []any{
		map[string]any{"ok": true, "result": map[string]any{"available": false, "recordAvailable": true, "status": "record_available"}},
		map[string]any{"ok": true, "result": map[string]any{"items": []any{map[string]any{"status": "not_available"}}}},
		map[string]any{"result": testToolResultEnvelope{result: map[string]any{"records": []any{map[string]any{"status": "not_available"}}}}},
		map[string]any{"result": map[string]any{"result": map[string]any{"status": "not_available"}}},
		map[string]any{"ok": true, "results": []any{}},
	} {
		if got := ClassifyToolResult(value); got != ToolResultSucceeded || toolResultReportsNoProgress(value) {
			t.Errorf("ordinary data became an unavailable envelope: %s %#v", got, value)
		}
	}
	if got := ClassifyToolResult(map[string]any{"sources": []any{
		map[string]any{"status": "not_available"}, map[string]any{"status": "fetched"},
	}}); got != ToolResultPartial {
		t.Fatalf("mixed availability outcome = %s", got)
	}
}

func TestEngineUnavailableLookupsUseExistingRecoveryBoundary(t *testing.T) {
	for _, freshMiddle := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh_middle_%t", freshMiddle), func(t *testing.T) {
			responses := make([]ModelResponse, 0, 4)
			for index := 0; index < 3; index++ {
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
					ID: fmt.Sprintf("lookup-%d", index), Name: "lookup_record",
					Arguments: json.RawMessage(fmt.Sprintf(`{"id":"record-%d"}`, index)),
				}}}})
			}
			responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "The retrieved record is available for review."}})
			model := &capturingRequestModelClient{responses: responses}
			var events []Event
			engine := Engine{Model: model, Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
				status := "not_available"
				if freshMiddle && call.ID == "lookup-1" {
					status = "record_available"
				}
				return ToolResult{Value: map[string]any{"ok": true, "result": testToolResultEnvelope{
					result: map[string]any{"status": status},
				}}}, nil
			}), OnEvent: func(event Event) { events = append(events, event) }}
			_, err := engine.Run(context.Background(), RunRequest{
				Messages: []Message{{Role: "user", Content: "Read the requested records."}},
				Tools:    []ToolSchema{{Name: "lookup_record"}}, MaxConsecutiveIdenticalToolRounds: 3,
			})
			var noProgress *ToolRoundNoProgressError
			if freshMiddle {
				if err != nil || len(model.requests) != 4 {
					t.Fatalf("new usable record failed to reset recovery window: requests=%d err=%v", len(model.requests), err)
				}
			} else if !errors.As(err, &noProgress) || len(noProgress.Calls) != 3 || len(model.requests) != 3 {
				t.Fatalf("distinct unavailable lookups escaped existing boundary: requests=%d err=%v", len(model.requests), err)
			}
			for _, event := range events {
				if event.Type == EventToolFailed {
					t.Fatal("source unavailability became a hard tool failure")
				}
			}
			for _, request := range model.requests {
				for _, message := range request.Messages {
					if message.Role == "system" && strings.Contains(message.Content, "only idempotent receipts") {
						t.Fatal("unavailable receipt was presented as reusable success")
					}
				}
			}
		})
	}
}

func TestWrappedUnavailableSourcePreservesRetryContract(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(fmt.Sprintf("typed_%t", typed), func(t *testing.T) {
			source := map[string]any{"sourceUnavailable": true, "retryable": true, "status": "source_unavailable"}
			var value any = source
			if typed {
				value = testToolResultEnvelope{result: source}
			}
			wrapped := map[string]any{"ok": true, "result": value}
			calls := 0
			guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				calls++
				if calls == 1 {
					return ToolResult{Value: wrapped}, nil
				}
				return ToolResult{Value: map[string]any{"ok": true, "body": "Recovered source"}}, nil
			}), ToolSchema{Name: "lookup_record", Capabilities: []string{"source-evidence"}})
			call := ToolCall{Name: "lookup_record", Arguments: json.RawMessage(`{"id":"record"}`)}
			if _, err := guard.Execute(context.Background(), call); err != nil {
				t.Fatal(err)
			}
			result, err := guard.Execute(context.Background(), call)
			if err != nil || calls != 2 || ClassifyToolResult(result.Value) != ToolResultSucceeded {
				t.Fatalf("transient source poisoned exact retry: calls=%d result=%#v err=%v", calls, result.Value, err)
			}
			wrapped["retryable"] = false
			if isRetryableSourceUnavailable(wrapped) {
				t.Fatal("outer non-retryable boundary was ignored")
			}
		})
	}
}

func TestWrappedUnavailableSourceRetainsBoundedConnectorRetries(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(fmt.Sprintf("typed_%t", typed), func(t *testing.T) {
			calls := 0
			guard := newFailedToolCallGuard(FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				calls++
				source := map[string]any{"sourceUnavailable": true, "retryable": true}
				var value any = source
				if typed {
					value = testToolResultEnvelope{result: source}
				}
				return ToolResult{Value: map[string]any{"ok": true, "result": value}}, nil
			}), ToolSchema{Name: "lookup_record", Capabilities: []string{"source-evidence"}})
			for index := 0; index < maxTransientSourceUnavailableAttempts; index++ {
				_, err := guard.Execute(context.Background(), ToolCall{Name: "lookup_record", Arguments: json.RawMessage(fmt.Sprintf(`{"id":%d}`, index))})
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := guard.Execute(context.Background(), ToolCall{Name: "lookup_record", Arguments: json.RawMessage(`{"id":"new-input"}`)})
			value := toolResultEnvelopeMap(result.Value)
			if err != nil || calls != maxTransientSourceUnavailableAttempts || value["code"] != "source_unavailable_retry_exhausted" || value["executed"] != false {
				t.Fatalf("wrapped source bypassed connector budget: calls=%d result=%#v err=%v", calls, value, err)
			}
		})
	}
}
