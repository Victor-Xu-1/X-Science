package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestProviderPlanningPartialToolBoundaryIsNotNoNativeProgress(t *testing.T) {
	// Use the same native event shapes as runtime_stream_test,
	// openai_responses_stream_test and native_stream_test, without a terminal
	// response. A begun tool call is progress, but never an executable receipt.
	for _, fixture := range []struct {
		protocol string
		start    []string
		private  string
	}{
		{
			protocol: ProtocolOpenAICompatible,
			start: []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
			},
			private: `{"choices":[{"delta":{"reasoning_content":"private"}}]}`,
		},
		{
			protocol: ProtocolOpenAIResponses,
			start: []string{
				`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`,
				`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"q\":"}`,
			},
			private: `{"type":"response.reasoning_summary_text.delta","delta":"private"}`,
		},
		{
			protocol: ProtocolAnthropic,
			start: []string{
				`{"type":"message_start","message":{"id":"msg_1","role":"assistant"}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
			},
			private: `{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"private"}}`,
		},
		{
			protocol: ProtocolGemini,
			start: []string{
				// Gemini frames arguments as objects, not JSON fragments. The call
				// still has no authority to execute until a terminal response.
				`{"responseId":"gem_1","candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":"x"}}}]}}]}`,
			},
			private: `{"responseId":"gem_1","candidates":[{"content":{"parts":[{"text":"private","thought":true}]}}]}`,
		},
	} {
		t.Run(fixture.protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range fixture.start {
					fmt.Fprintf(w, "data: %s\n\n", event)
				}
				fmt.Fprintf(w, "data: %s\n\n", fixture.private)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				stop := time.NewTimer(2 * time.Second)
				defer stop.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-stop.C:
						return
					case <-ticker.C:
						fmt.Fprintf(w, "data: %s\n\n", fixture.private)
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer server.Close()
			endpoint := server.URL
			if fixture.protocol == ProtocolGemini {
				endpoint += "/v1beta/models/model:generateContent"
			}
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "partial-tool-planning", Protocol: fixture.protocol, Endpoint: endpoint},
				Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
			}, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var text strings.Builder
			boundaries, reasoning := 0, 0
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Look up the requested record."}},
				Tools:    []agentruntime.ToolSchema{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
					boundaries++
				}
				if event.ReasoningActive {
					reasoning++
				}
				text.WriteString(event.ContentDelta)
				return nil
			})
			if !IsProviderPlanningNoProgress(err) || !IsProviderEmptyResponse(err) || IsProviderPlanningNoNativeProgress(err) || IsRecoverableStreamInterruption(err) {
				t.Fatalf("partial native action was classified as absent or completed: %v", err)
			}
			if boundaries != 1 || reasoning == 0 || text.Len() != 0 || len(response.Message.ToolCalls) != 0 || response.Message.Content != "" {
				t.Fatalf("partial action leaked or boundary was lost: boundaries=%d reasoning=%d text=%q response=%+v", boundaries, reasoning, text.String(), response)
			}
			if requests.Load() != 1 || ctx.Err() != nil {
				t.Fatalf("operation deadline replayed partial action or used parent cancellation: requests=%d parent=%v", requests.Load(), ctx.Err())
			}
		})
	}
}
