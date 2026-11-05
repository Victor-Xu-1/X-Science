package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func textToolBoundaryProviderForTest(t *testing.T, prefix, arguments string, native bool) (agentruntime.ModelClient, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence := requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["stream"] != true {
			t.Errorf("fixture did not receive a real streaming request: stream=%v err=%v", body["stream"], err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if sequence > 2 {
			t.Error("unexpected repeated generation after boundary recovery")
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta map[string]any, finish any) {
			chunk, err := json.Marshal(map[string]any{"id": "synthetic-boundary-response", "choices": []any{
				map[string]any{"index": 0, "delta": delta, "finish_reason": finish},
			}})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			w.(http.Flusher).Flush()
		}
		if sequence == 2 {
			write(map[string]any{"role": "assistant", "content": "Verified complete."}, "stop")
		} else {
			write(map[string]any{"role": "assistant", "content": prefix}, nil)
			if native {
				// The provider emits its native boundary after this first fragment.
				// The complete arguments only arrive in the following SSE chunk.
				split := len(arguments) / 2
				write(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "native-inspection", "type": "function",
					"function": map[string]any{"name": "inspect", "arguments": arguments[:split]},
				}}}, nil)
				write(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "function": map[string]any{"arguments": arguments[split:]},
				}}}, "tool_calls")
			} else {
				write(map[string]any{}, "stop")
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(origin.Close)
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "boundary-fixture", Type: "openai", Protocol: ProtocolOpenAICompatible, Endpoint: origin.URL},
		Model:    "synthetic-boundary-model", APIKey: "fixture-boundary-key",
		Request: RequestProfile{Timeout: 3 * time.Second, MaxAttempts: 1, MaxResponseBytes: 64 << 10},
	}, origin.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client, requests
}

func TestRuntimeStreamRetainsTextProbeFailureAtNativeBoundary(t *testing.T) {
	for _, test := range []struct {
		name, arguments string
		native          bool
		wantCalls       int
	}{
		{name: "valid_native", native: true, arguments: `{"value":"BOUNDARY_OBSERVED_VALUE"}`, wantCalls: 1},
		{name: "invalid_native_arguments", native: true, arguments: `{broken`},
		{name: "no_native_terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, requests := textToolBoundaryProviderForTest(t, "<tool_call><function=inspect", test.arguments, test.native)
			calls := 0
			var published strings.Builder
			engine := agentruntime.Engine{Model: client,
				Tools: agentruntime.FuncToolGateway(func(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
					calls++
					if call.Name != "inspect" || string(call.Arguments) != test.arguments {
						t.Errorf("native arguments were incomplete or changed: name=%s args=%s", call.Name, call.Arguments)
					}
					return agentruntime.ToolResult{Value: map[string]any{"ok": true}}, nil
				}), OnModelDelta: func(event agentruntime.ModelStreamEvent) error {
					published.WriteString(event.ContentDelta)
					return nil
				}}
			result, err := engine.Run(t.Context(), agentruntime.RunRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Perform the authorized inspection."}},
				Tools:    []agentruntime.ToolSchema{{Name: "inspect", Parameters: map[string]any{"type": "object"}}}, MaxToolRounds: 2,
			})
			if calls != test.wantCalls || strings.Contains(published.String(), "<tool_call>") || strings.Contains(published.String(), "BOUNDARY_OBSERVED_VALUE") {
				t.Fatalf("text was exposed or native admission changed: calls=%d want=%d published=%q", calls, test.wantCalls, published.String())
			}
			if !test.native {
				var invalid *agentruntime.TextToolEnvelopePresentationError
				if !errors.As(err, &invalid) || result.FinalMessage.Content != "" || requests.Load() != 1 {
					t.Fatalf("unexecuted text became final or retried: error=%v requests=%d", err, requests.Load())
				}
			} else if err != nil || result.FinalMessage.Content != "Verified complete." || requests.Load() != 2 {
				t.Fatalf("native boundary aborted collection: error=%v final=%q requests=%d", err, result.FinalMessage.Content, requests.Load())
			}
		})
	}
}

func TestRuntimeStreamBoundaryPreservesCancellationAndPublicationErrors(t *testing.T) {
	for _, mode := range []string{"cancel", "publication"} {
		t.Run(mode, func(t *testing.T) {
			client, requests := textToolBoundaryProviderForTest(t, "Safe narration.", `{"value":"unused"}`, true)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// A downstream publication failure can itself have a presentation
			// error type. Its wrapper must keep it fatal rather than retain it.
			cause := &agentruntime.TextToolEnvelopePresentationError{}
			calls := 0
			engine := agentruntime.Engine{Model: client,
				Tools: agentruntime.FuncToolGateway(func(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
					calls++
					return agentruntime.ToolResult{}, nil
				}), OnModelDelta: func(event agentruntime.ModelStreamEvent) error {
					if event.Kind != agentruntime.ModelStreamEventToolCallBoundary {
						return nil
					}
					if mode == "cancel" {
						cancel()
						return context.Canceled
					}
					return cause
				}}
			result, err := engine.Run(ctx, agentruntime.RunRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Inspect once."}},
				Tools:    []agentruntime.ToolSchema{{Name: "inspect"}}, MaxToolRounds: 1,
			})
			want := error(cause)
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || calls != 0 || requests.Load() != 1 || result.FinalMessage.Content != "" {
				t.Fatalf("fatal boundary error swallowed: err=%v calls=%d requests=%d final=%q", err, calls, requests.Load(), result.FinalMessage.Content)
			}
		})
	}
}
