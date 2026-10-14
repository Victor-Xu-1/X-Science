package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

const responsesCompletedTextFixture = `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`
const responsesCompletedToolFixture = `{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"function_call","id":"item","call_id":"call","name":"lookup","arguments":"{}"}]}}`

func TestProviderResponsesCompletedGrowthCountsOnlyAdmittedDifference(t *testing.T) {
	for _, test := range []struct {
		name, prefix, completed string
		wantBytes               int64
		wantError               bool
	}{
		{name: "text without delta", completed: responsesCompletedTextFixture, wantBytes: 4},
		{name: "tool without delta", completed: responsesCompletedToolFixture, wantBytes: int64(len("call") + len("lookup") + len("{}"))},
		{name: "additional text", prefix: `{"type":"response.output_text.delta","delta":"do"}`, completed: responsesCompletedTextFixture, wantBytes: 4},
		{name: "unchanged snapshot", prefix: `{"type":"response.output_text.delta","delta":"done"}`, completed: responsesCompletedTextFixture, wantBytes: 4},
		{name: "duplicate terminal rejected", prefix: responsesCompletedTextFixture, completed: responsesCompletedTextFixture, wantBytes: 4, wantError: true},
		{name: "invalid tool rejected", completed: strings.Replace(responsesCompletedToolFixture, `"name":"lookup"`, `"name":""`, 1), wantError: true},
		{name: "incomplete terminal rejected", completed: strings.Replace(responsesCompletedTextFixture, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`, 1), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var input strings.Builder
			if test.prefix != "" {
				fmt.Fprintf(&input, "data: %s\n\n", test.prefix)
			}
			fmt.Fprintf(&input, "data: %s\n\n", test.completed)
			reader := &generationProgressReader{Reader: strings.NewReader(input.String())}
			_, _, _, _, err := readOpenAIResponsesStream(reader, 4096, nil, (&runtimeModelClient{}).openAIResponsesResponseDecoder)
			if (err != nil) != test.wantError || reader.bytes != test.wantBytes {
				t.Fatalf("completed growth=%d want=%d err=%v wantError=%v", reader.bytes, test.wantBytes, err, test.wantError)
			}
		})
	}
}

func TestProviderResponsesCompletedGrowthHandsOffInitialDeadline(t *testing.T) {
	const window = 300 * time.Millisecond
	for _, test := range []struct {
		name, completed, content string
		calls                    int
	}{
		{name: "text", completed: responsesCompletedTextFixture, content: "done"},
		{name: "tool", completed: responsesCompletedToolFixture, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.(http.Flusher).Flush()
				for _, step := range []struct {
					delay time.Duration
					data  string
				}{
					{window / 2, test.completed},
					{window * 2 / 3, "[DONE]"},
				} {
					timer := time.NewTimer(step.delay)
					select {
					case <-r.Context().Done():
						timer.Stop()
						return
					case <-timer.C:
					}
					fmt.Fprintf(w, "data: %s\n\n", step.data)
					w.(http.Flusher).Flush()
				}
			}))
			defer api.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "completed-growth-fixture", Protocol: ProtocolOpenAIResponses, Endpoint: api.URL},
				Model:    "fixture", Request: RequestProfile{Timeout: window, MaxAttempts: 1, MaxResponseBytes: 4096},
			}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Tools: []agentruntime.ToolSchema{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
			}, nil)
			if err != nil || ctx.Err() != nil || response.Message.Content != test.content || len(response.Message.ToolCalls) != test.calls {
				t.Fatalf("completed response lost before EOF: err=%v parent=%v content=%q calls=%v", err, ctx.Err(), response.Message.Content, response.Message.ToolCalls)
			}
		})
	}
}
