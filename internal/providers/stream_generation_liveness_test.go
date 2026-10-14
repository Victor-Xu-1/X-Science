package providers

import (
	"context"
	"errors"
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

func TestProviderRepeatedTerminalDoesNotRenewGeneration(t *testing.T) {
	for _, fixture := range []struct {
		protocol string
		prefix   string
		terminal string
	}{
		{ProtocolOpenAICompatible,
			`{"choices":[{"delta":{"content":"public fixture"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`},
		{ProtocolAnthropic,
			`{"type":"message_start","message":{"id":"msg","role":"assistant"}}` + "\n\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"public fixture"}}`,
			`{"type":"message_stop"}`},
		{ProtocolGemini,
			`{"candidates":[{"content":{"parts":[{"text":"public fixture"}]}}]}`,
			`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`},
	} {
		t.Run(fixture.protocol, func(t *testing.T) {
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: %s\n\n", fixture.prefix, fixture.terminal)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						fmt.Fprintf(w, "data: %s\n\n", fixture.terminal)
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer api.Close()
			endpoint := api.URL
			if fixture.protocol == ProtocolGemini {
				endpoint += "/v1beta/models/model:generateContent"
			}
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "terminal-fixture", Protocol: fixture.protocol, Endpoint: endpoint},
				Model:    "fixture", Request: RequestProfile{Timeout: 60 * time.Millisecond, MaxAttempts: 1, MaxResponseBytes: 4096},
			}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			// This outer deadline only releases a broken controlled stream; it is
			// not accepted as the product's idle boundary or a generation default.
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Tools: []agentruntime.ToolSchema{{Name: "fixture", Parameters: map[string]any{"type": "object"}}},
			}, nil)
			if !errors.Is(err, errOpenAIChatStreamIdleTimeout) || ctx.Err() != nil || requests.Load() != 1 {
				t.Fatalf("zero-growth terminal renewed generation: err=%v parent=%v requests=%d", err, ctx.Err(), requests.Load())
			}
		})
	}
}

func TestProviderPrivateGrowthCanReachACompletedAction(t *testing.T) {
	for _, fixture := range []struct {
		protocol string
		initial  string
		private  string
		ending   string
	}{
		{ProtocolOpenAICompatible, "",
			`{"choices":[{"delta":{"reasoning_content":"private fixture"}}]}`,
			`{"choices":[{"delta":{"content":"ready"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]"},
		{ProtocolAnthropic,
			`{"type":"message_start","message":{"id":"msg","role":"assistant"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private fixture"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ready"}}` + "\n\ndata: " + `{"type":"message_stop"}`},
		{ProtocolGemini, "",
			`{"candidates":[{"content":{"parts":[{"text":"private fixture","thought":true}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"ready"}]},"finishReason":"STOP"}]}`},
		{ProtocolOpenAIResponses, "",
			`{"type":"response.reasoning_summary_text.delta","delta":"private fixture"}`,
			`{"type":"response.output_text.delta","delta":"ready"}` + "\n\ndata: " +
				`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}}`},
	} {
		t.Run(fixture.protocol, func(t *testing.T) {
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if fixture.initial != "" {
					fmt.Fprintf(w, "data: %s\n\n", fixture.initial)
				}
				ticker := time.NewTicker(15 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; i < 12; i++ {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						fmt.Fprintf(w, "data: %s\n\n", fixture.private)
						w.(http.Flusher).Flush()
					}
				}
				fmt.Fprintf(w, "data: %s\n\n", fixture.ending)
			}))
			defer api.Close()
			endpoint := api.URL
			if fixture.protocol == ProtocolGemini {
				endpoint += "/v1beta/models/model:generateContent"
			}
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "private-growth-fixture", Protocol: fixture.protocol, Endpoint: endpoint},
				Model:    "fixture", Request: RequestProfile{Timeout: 60 * time.Millisecond, MaxAttempts: 1, MaxResponseBytes: 4096},
			}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var public strings.Builder
			privateCallbacks := 0
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Tools: []agentruntime.ToolSchema{{Name: "fixture", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error {
				public.WriteString(event.ContentDelta)
				if event.ReasoningActive {
					privateCallbacks++
				}
				return nil
			})
			if err != nil || ctx.Err() != nil || requests.Load() != 1 || public.String() != "ready" || response.Message.Content != "ready" || privateCallbacks != 12 {
				t.Fatalf("active private generation did not complete privately: err=%v parent=%v requests=%d public=%q response=%q callbacks=%d", err, ctx.Err(), requests.Load(), public.String(), response.Message.Content, privateCallbacks)
			}
		})
	}
}

func TestProviderPrivateActivityExhaustionKeepsFiniteNoActionRecovery(t *testing.T) {
	for _, fixture := range []struct{ protocol, initial, private string }{
		{ProtocolOpenAICompatible, "", `{"choices":[{"delta":{"reasoning_content":"private fixture"}}]}`},
		{ProtocolAnthropic, `{"type":"message_start","message":{"id":"msg","role":"assistant"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private fixture"}}`},
		{ProtocolGemini, "", `{"candidates":[{"content":{"parts":[{"text":"private fixture","thought":true}]}}]}`},
		{ProtocolOpenAIResponses, "", `{"type":"response.reasoning_summary_text.delta","delta":"private fixture"}`},
	} {
		for _, idle := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/idle_%t", fixture.protocol, idle), func(t *testing.T) {
				var requests atomic.Int32
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if fixture.initial != "" {
						fmt.Fprintf(w, "data: %s\n\n", fixture.initial)
					}
					fmt.Fprintf(w, "data: %s\n\n", fixture.private)
					w.(http.Flusher).Flush()
					if idle {
						<-r.Context().Done()
						return
					}
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-r.Context().Done():
							return
						case <-ticker.C:
							fmt.Fprintf(w, "data: %s\n\n", fixture.private)
							w.(http.Flusher).Flush()
						}
					}
				}))
				defer api.Close()
				endpoint := api.URL
				if fixture.protocol == ProtocolGemini {
					endpoint += "/v1beta/models/model:generateContent"
				}
				client, err := NewRuntimeModelClient(ModelProfile{
					Provider: ProviderProfile{ID: "private-boundary", Protocol: fixture.protocol, Endpoint: endpoint},
					Model:    "fixture", Request: RequestProfile{Timeout: 60 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 256},
				}, api.Client(), nil)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				var public strings.Builder
				response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
					Tools: []agentruntime.ToolSchema{{Name: "fixture"}},
				}, func(event agentruntime.ModelStreamEvent) error { public.WriteString(event.ContentDelta); return nil })
				cause := errProviderResponseTooLarge
				if idle {
					cause = errOpenAIChatStreamIdleTimeout
				}
				if !IsProviderGenerationNoAction(err) || !IsProviderGenerationNoNativeAction(err) || !IsProviderEmptyResponse(err) ||
					!errors.Is(err, cause) || IsProviderPlanningNoProgress(err) || requests.Load() != 1 || ctx.Err() != nil || public.Len() != 0 || len(response.Message.ToolCalls) != 0 {
					t.Fatalf("private boundary lost bounded recovery/privacy: err=%v parent=%v requests=%d public=%q calls=%v", err, ctx.Err(), requests.Load(), public.String(), response.Message.ToolCalls)
				}
			})
		}
	}
}
