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

func TestOpenAIChatMalformedOptionalUsagePreservesCompletion(t *testing.T) {
	cases := []struct {
		name           string
		counter        any
		invalidCounter bool
		invalidPrimary bool
	}{
		{name: "valid-counter", counter: 3},
		{name: "string-counter", counter: "3", invalidCounter: true},
		{name: "overflow-counter", counter: json.Number("9223372036854775808"), invalidCounter: true},
		{name: "invalid-primary-id", counter: 3, invalidPrimary: true},
	}
	for _, stream := range []bool{false, true} {
		transport := "json"
		if stream {
			transport = "sse"
		}
		for _, test := range cases {
			t.Run(transport+"/"+test.name, func(t *testing.T) {
				usage := map[string]any{"prompt_tokens": 20, "completion_tokens": test.counter}
				if !test.invalidCounter {
					usage["total_tokens"] = 23
				}
				var responseID any = "usage-request"
				if test.invalidPrimary {
					responseID = 17
				}
				var calls atomic.Int32
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var incoming struct {
						Stream bool `json:"stream"`
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" ||
						json.NewDecoder(r.Body).Decode(&incoming) != nil || incoming.Stream != stream {
						t.Error("fixture received an unexpected request")
						http.Error(w, "invalid fixture request", http.StatusBadRequest)
						return
					}
					writeOptionalUsageDecodeFixture(t, w, stream, responseID, usage)
				}))
				defer provider.Close()
				var audits []AuditRecord
				client, err := NewRuntimeModelClient(ModelProfile{
					Provider: ProviderProfile{ID: "usage-decode", Type: "fixture", Protocol: ProtocolOpenAICompatible,
						Endpoint: provider.URL + "/v1/chat/completions"},
					Model: "usage-fixture-model",
					Request: RequestProfile{Timeout: 2 * time.Second, MaxAttempts: 1,
						MaxResponseBytes: 64 * 1024},
				}, provider.Client(), func(record AuditRecord) { audits = append(audits, record) })
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "inspect a record"}}}
				var response agentruntime.ModelResponse
				var emitted strings.Builder
				deltas := 0
				if stream {
					response, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, request, func(event agentruntime.ModelStreamEvent) error {
						if event.Kind == agentruntime.ModelStreamEventContentDelta {
							emitted.WriteString(event.ContentDelta)
							deltas++
						}
						return nil
					})
				} else {
					response, err = client.Complete(ctx, request)
				}
				if calls.Load() != 1 || len(audits) != 1 || audits[0].Attempt != 1 || audits[0].HTTPStatus != http.StatusOK {
					t.Fatalf("completion must make one actual request and audit: calls=%d audits=%+v", calls.Load(), audits)
				}
				if stream && (emitted.String() != "done" || deltas != 1) {
					t.Fatalf("valid primary text must arrive once before the usage trailer: text=%q deltas=%d", emitted.String(), deltas)
				}
				if test.invalidPrimary {
					var typeError *json.UnmarshalTypeError
					if !errors.As(err, &typeError) || typeError.Field != "id" || audits[0].Error == "" {
						t.Fatalf("malformed primary ID must remain a hard decode error: err=%v audit=%+v", err, audits[0])
					}
					return
				}
				if err != nil {
					t.Fatalf("optional usage counter must not reject a valid completed answer: %v", err)
				}
				if response.Message.Role != "assistant" || response.Message.Content != "done" ||
					response.RequestID != "usage-request" || response.Model != "usage-fixture-model" ||
					response.StopReason != "stop" || len(response.Message.ToolCalls) != 0 || audits[0].Error != "" {
					t.Fatalf("primary completion changed: response=%+v audit=%+v", response, audits[0])
				}
				wantFields := agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}
				wantOutput, wantTotal := 3, 23
				if test.invalidCounter {
					wantFields = agentruntime.ModelUsageFields{Input: true}
					wantOutput, wantTotal = 0, 0
				}
				if response.Usage.Reported == nil || response.Usage.ReportedFields() != wantFields ||
					response.Usage.InputTokens != 20 || response.Usage.OutputTokens != wantOutput ||
					response.Usage.TotalTokens != wantTotal || audits[0].PromptTokens != 20 ||
					audits[0].CompletionTokens != wantOutput || audits[0].TotalTokens != wantTotal {
					t.Fatalf("invalid counters must not become measured values or erase valid input: usage=%+v audit=%+v", response.Usage, audits[0])
				}
			})
		}
	}
}

func writeOptionalUsageDecodeFixture(t *testing.T, w http.ResponseWriter, stream bool, id any, usage map[string]any) {
	t.Helper()
	w.Header().Set("X-Request-ID", "usage-request")
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		body := usagePresenceBody(ProtocolOpenAICompatible, usage)
		body["id"], body["model"] = id, "usage-fixture-model"
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("fixture JSON encoding: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(value any) bool {
		raw, err := json.Marshal(value)
		if err == nil {
			_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		if err != nil {
			t.Errorf("fixture SSE encoding: %v", err)
		}
		return err == nil
	}
	if !emit(map[string]any{"id": "usage-request", "choices": []any{map[string]any{
		"index": 0, "delta": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop",
	}}}) {
		return
	}
	w.(http.Flusher).Flush()
	if emit(map[string]any{"id": id, "choices": []any{}, "usage": usage}) {
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Errorf("fixture SSE terminal: %v", err)
		}
	}
}
