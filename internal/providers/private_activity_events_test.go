package providers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestProviderPrivateActivityEventsRetainEveryNonemptyFragment(t *testing.T) {
	// Real protocol parsers see synthetic private fragments, including identical
	// adjacent fragments. Only their activity, never their text, leaves the parser.
	for _, fixture := range []struct {
		protocol string
		private  []string
		finish   []string
	}{
		{
			protocol: ProtocolOpenAICompatible,
			private: []string{
				`{"choices":[{"delta":{"reasoning_content":""}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"private-marker"}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"private-marker"}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"private-marker"}}]}`,
			},
			finish: []string{`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`, `[DONE]`},
		},
		{
			protocol: ProtocolOpenAIResponses,
			private: []string{
				`{"type":"response.reasoning_text.delta","delta":""}`,
				`{"type":"response.reasoning_text.delta","delta":"private-marker"}`,
				`{"type":"response.reasoning_summary_text.delta","delta":"private-marker"}`,
				`{"type":"response.reasoning_summary_text.delta","delta":"private-marker"}`,
			},
			finish: []string{
				`{"type":"response.output_text.delta","delta":"done"}`,
				`{"type":"response.completed","response":{"id":"response-1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`,
			},
		},
		{
			protocol: ProtocolAnthropic,
			private: []string{
				`{"type":"message_start","message":{"id":"message-1","role":"assistant"}}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":""}}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"private-marker"}}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"private-marker"}}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"private-marker"}}`,
			},
			finish: []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"done"}}`, `{"type":"message_stop"}`},
		},
		{
			protocol: ProtocolGemini,
			private: []string{
				`{"candidates":[{"content":{"parts":[{"thought":true,"text":""}]}}]}`,
				`{"candidates":[{"content":{"parts":[{"thought":true,"text":"private-marker"}]}}]}`,
				`{"candidates":[{"content":{"parts":[{"thought":true,"text":"private-marker"},{"thought":true,"text":"private-marker"}]}}]}`,
			},
			finish: []string{`{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`},
		},
	} {
		for _, outcome := range []string{"completed", "incomplete", "consumer-stopped"} {
			t.Run(fixture.protocol+"/"+outcome, func(t *testing.T) {
				var stream strings.Builder
				for _, event := range fixture.private {
					stream.WriteString("data: " + event + "\n\n")
				}
				if outcome != "incomplete" {
					for _, event := range fixture.finish {
						stream.WriteString("data: " + event + "\n\n")
					}
				}
				activity, publicEvents := 0, 0
				var public strings.Builder
				stopped := errors.New("activity consumer stopped")
				emit := func(event agentruntime.ModelStreamEvent) error {
					if err := agentruntime.ValidateModelStreamEvent(event); err != nil {
						t.Fatalf("invalid semantic event: %v", err)
					}
					if event.ReasoningActive {
						activity++
						if event.ContentDelta != "" || event.BlockID != "" {
							t.Fatal("private activity carried publishable content")
						}
						if outcome == "consumer-stopped" && activity == 2 {
							return stopped
						}
					} else {
						publicEvents++
						public.WriteString(event.ContentDelta)
					}
					return nil
				}
				reader := strings.NewReader(stream.String())
				client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: 64 << 10}}}
				response := http.Response{Body: io.NopCloser(reader)}
				var result agentruntime.ModelResponse
				var emitted bool
				var err error
				switch fixture.protocol {
				case ProtocolOpenAICompatible:
					result, _, _, emitted, err = readOpenAIChatStream(reader, 64<<10, emit)
				case ProtocolOpenAIResponses:
					result, _, _, emitted, err = readOpenAIResponsesStream(reader, 64<<10, emit, client.openAIResponsesResponseDecoder)
				case ProtocolAnthropic:
					result, _, _, emitted, err = readAnthropicStream(client, response, emit)
				case ProtocolGemini:
					result, _, _, emitted, err = readGeminiStream(client, response, emit)
				}
				wantActivity := 3
				if outcome == "consumer-stopped" {
					wantActivity = 2
					if !errors.Is(err, stopped) {
						t.Fatalf("consumer error was not retained: %v", err)
					}
				}
				if activity != wantActivity {
					t.Fatalf("private activity callbacks=%d, want %d", activity, wantActivity)
				}
				if outcome == "completed" {
					if err != nil || !emitted || publicEvents != 1 || public.String() != "done" || result.Message.Content != "done" {
						t.Fatalf("public output changed: emitted=%t events=%d err=%v", emitted, publicEvents, err)
					}
				} else if err == nil || emitted || publicEvents != 0 || result.Message.Content != "" || len(result.Message.ToolCalls) != 0 {
					t.Fatalf("private-only interruption fabricated output: emitted=%t publicEvents=%d err=%v", emitted, publicEvents, err)
				}
			})
		}
	}
}
