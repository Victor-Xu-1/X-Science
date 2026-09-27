package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Use a real protocol boundary: the service declines admission during an
// outage, then becomes available. Only the accepted request performs work.
func TestRuntimePreflightRecoveryContinuesAfterServiceRestored(t *testing.T) {
	var requests, executions atomic.Int64
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ok":false,"code":"software_runtime_unavailable","executed":false,"preflight":true,"retryable":true,"message":"Runtime is preparing; no operation was started."}`))
			return
		}
		executions.Add(1)
		_, _ = w.Write([]byte(`{"ok":true,"executed":true,"output":"computed result"}`))
	}))
	defer service.Close()
	gateway := FuncToolGateway(func(ctx context.Context, _ ToolCall) (ToolResult, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.URL, nil)
		if err != nil {
			return ToolResult{}, err
		}
		response, err := service.Client().Do(request)
		if err != nil {
			return ToolResult{}, err
		}
		defer response.Body.Close()
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: value}, nil
	})
	guard := newFailedToolCallGuard(gateway, ToolSchema{Name: "execute_analysis", Capabilities: []string{"runtime-execution"}})
	call := ToolCall{ID: "first", Name: "execute_analysis", Arguments: json.RawMessage(`{"input":"dataset"}`)}
	first, err := guard.Execute(context.Background(), call)
	value, _ := first.Value.(map[string]any)
	if err != nil || value["executed"] != false || value["terminal"] == true || value["retryable"] != true {
		t.Fatalf("nonexecuted admission was converted to a terminal job: %#v err=%v", value, err)
	}
	call.ID = "restored"
	second, err := guard.Execute(context.Background(), call)
	if err != nil || ClassifyToolResult(second.Value) != ToolResultSucceeded || requests.Load() != 2 || executions.Load() != 1 {
		t.Fatalf("restored service could not resume exact work: result=%#v err=%v requests=%d executions=%d",
			second.Value, err, requests.Load(), executions.Load())
	}
}

func TestRuntimePreflightRecoveryUsesExistingNoProgressBudget(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprintf("recover_%t", recover), func(t *testing.T) {
			responses := make([]ModelResponse, 0, 4)
			for index := 0; index < 3; index++ {
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
					ID: fmt.Sprintf("attempt-%d", index), Name: "execute_analysis", Arguments: json.RawMessage(`{"input":"dataset"}`),
				}}}})
			}
			responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "Completed."}})
			model := &capturingRequestModelClient{responses: responses}
			calls, executions := 0, 0
			engine := Engine{Model: model, Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				calls++
				if recover && calls > 1 {
					executions++
					return ToolResult{Value: map[string]any{"ok": true, "executed": true, "output": "computed",
						"effect": ToolEffectValue(ToolEffectChanged, "execution")}}, nil
				}
				return ToolResult{Value: map[string]any{
					"ok": false, "executed": false, "preflight": true, "retryable": true,
					"code": "runtime_draining", "message": "Runtime is recovering; no operation was started.",
				}}, nil
			})}
			_, err := engine.Run(context.Background(), RunRequest{
				Messages:                          []Message{{Role: "user", Content: "Compute the dataset."}},
				Tools:                             []ToolSchema{{Name: "execute_analysis", Capabilities: []string{"runtime-execution"}}},
				MaxConsecutiveIdenticalToolRounds: 3,
			})
			if recover {
				if err != nil || executions == 0 {
					t.Fatalf("engine failed to continue without user intervention: calls=%d executed=%d err=%v", calls, executions, err)
				}
			} else {
				var stalled *ToolRoundNoProgressError
				if !errors.As(err, &stalled) || calls != 2 || executions != 0 {
					t.Fatalf("persistent outage escaped existing recovery window: calls=%d executed=%d err=%v", calls, executions, err)
				}
			}
		})
	}
}

func TestRuntimeContinuationPreservesMaterialProgressBeyondCallCount(t *testing.T) {
	for _, effect := range []string{"execution", "files"} {
		t.Run(effect, func(t *testing.T) {
			responses := []ModelResponse{}
			for index := 0; index < 6; index++ {
				responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
					ID: fmt.Sprintf("continue-%d", index), Name: "continue_work", Arguments: json.RawMessage(`{"job":"existing"}`),
				}}}})
			}
			responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "All segments completed."}})
			calls := 0
			engine := Engine{Model: &capturingRequestModelClient{responses: responses}, Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				calls++
				value := map[string]any{"ok": true, "executed": true}
				if effect == "execution" {
					value["effect"] = ToolEffectValue(ToolEffectChanged, "execution")
				} else {
					value["files_written"] = []any{fmt.Sprintf("segment-%d.dat", calls)}
				}
				return ToolResult{Value: value}, nil
			})}
			_, err := engine.Run(context.Background(), RunRequest{
				Messages:                          []Message{{Role: "user", Content: "Complete every segment."}},
				Tools:                             []ToolSchema{{Name: "continue_work", Capabilities: []string{"runtime-execution"}}},
				MaxConsecutiveIdenticalToolRounds: 3,
			})
			if err != nil || calls != 6 {
				t.Fatalf("active continuation was stopped by call count: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRuntimeInitialSchemaPreservesStartedExecutionBoundary(t *testing.T) {
	responses := []ModelResponse{}
	for index := 0; index < 2; index++ {
		responses = append(responses, ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: fmt.Sprintf("execution-%d", index), Name: "custom_executor", Arguments: json.RawMessage(`{"job":"existing"}`),
		}}}})
	}
	responses = append(responses, ModelResponse{Message: Message{Role: "assistant", Content: "Reconcile the existing operation."}})
	calls := 0
	engine := Engine{Model: &capturingRequestModelClient{responses: responses}, Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
		calls++
		return ToolResult{Value: map[string]any{"ok": false, "executed": true, "code": "runtime_draining", "error": "Runtime draining after launch"}}, nil
	})}
	_, err := engine.Run(context.Background(), RunRequest{
		Messages:                          []Message{{Role: "user", Content: "Complete the existing job."}},
		Tools:                             []ToolSchema{{Name: "custom_executor", Capabilities: []string{"runtime-execution"}}},
		MaxConsecutiveIdenticalToolRounds: 3,
	})
	if err != nil || calls != 1 {
		t.Fatalf("initial execution capability was lost: calls=%d err=%v", calls, err)
	}
}
