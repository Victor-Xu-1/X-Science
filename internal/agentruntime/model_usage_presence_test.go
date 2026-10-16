package agentruntime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIChatClientPreservesUsagePresence(t *testing.T) {
	cases := []struct {
		name, wire           string
		input, output, total int
		fields               ModelUsageFields
	}{
		{"output-only", `{"completion_tokens":10}`, 0, 10, 0, ModelUsageFields{Output: true}},
		{"explicit-zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, 0, 0, 0, ModelUsageFields{Input: true, Output: true, Total: true}},
		{"missing", `{}`, 0, 0, 0, ModelUsageFields{}},
		{"null-is-unknown", `{"prompt_tokens":null,"completion_tokens":null,"total_tokens":null}`, 0, 0, 0, ModelUsageFields{}},
		{"complete-components", `{"prompt_tokens":20,"completion_tokens":3}`, 20, 3, 23, ModelUsageFields{Input: true, Output: true}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}],"usage":%s}`, test.wire)
			}))
			defer provider.Close()
			client := OpenAIChatClient{Endpoint: provider.URL, Model: "usage-presence", MaxAttempts: 1, HTTPClient: provider.Client()}
			response, err := client.Complete(t.Context(), ModelRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
			if err != nil || calls != 1 || response.Usage.Reported == nil || response.Usage.ReportedFields() != test.fields ||
				response.Usage.InputTokens != test.input || response.Usage.OutputTokens != test.output || response.Usage.TotalTokens != test.total {
				t.Fatalf("counter presence was lost: calls=%d usage=%+v fields=%+v err=%v", calls, response.Usage, response.Usage.ReportedFields(), err)
			}
		})
	}
}
