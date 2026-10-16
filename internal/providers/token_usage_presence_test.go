package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestProviderUsagePresenceSurvivesJSONAndStream(t *testing.T) {
	protocols := []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini}
	cases := []struct {
		name                        string
		kind                        string
		input, output, total, cache int
		fields                      agentruntime.ModelUsageFields
	}{
		{name: "output-only", kind: "output", output: 10, fields: agentruntime.ModelUsageFields{Output: true}},
		{name: "explicit-output-zero", kind: "output", fields: agentruntime.ModelUsageFields{Output: true}},
		{name: "missing", kind: "missing"},
		{name: "null-counters", kind: "null"},
		{name: "explicit-all-zero", kind: "complete", fields: agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}},
		{name: "complete-cached", kind: "complete", input: 20, output: 3, total: 23, cache: 5, fields: agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}},
		{name: "components-without-total", kind: "components", input: 20, output: 3, total: 23, fields: agentruntime.ModelUsageFields{Input: true, Output: true}},
	}
	for _, protocol := range protocols {
		for _, stream := range []bool{false, true} {
			for _, test := range cases {
				name := fmt.Sprintf("%s/%v/%s", protocol, stream, test.name)
				t.Run(name, func(t *testing.T) {
					wire := usagePresenceWire(protocol, test.kind, test.input, test.output, test.total, test.cache)
					response, audit := completeUsagePresenceFixture(t, protocol, stream, wire, nil)
					wantTotal, wantFields := test.total, test.fields
					if protocol == ProtocolAnthropic {
						wantFields.Total = false // This protocol supplies components, not a total field.
						if wantFields.Input && wantFields.Output {
							wantTotal = test.input + test.output + test.cache
							wantFields.CacheWrite = test.kind == "complete"
						}
					}
					usage := response.Usage
					if usage.Reported == nil || usage.ReportedFields() != wantFields ||
						usage.InputTokens != test.input || usage.OutputTokens != test.output ||
						usage.TotalTokens != wantTotal || usage.CacheReadTokens != test.cache ||
						usage.CacheTokensAdditive != (protocol == ProtocolAnthropic) {
						t.Fatalf("presence/counters: usage=%+v fields=%+v, want fields=%+v total=%d", usage, usage.ReportedFields(), wantFields, wantTotal)
					}
					if audit.PromptTokens != usage.InputTokens || audit.CompletionTokens != usage.OutputTokens ||
						audit.TotalTokens != usage.TotalTokens || audit.CacheReadTokens != usage.CacheReadTokens || audit.Error != "" {
						t.Fatalf("response and audit counters diverged: usage=%+v audit=%+v", usage, audit)
					}
				})
			}
		}
	}
}

func TestProviderUsageStreamPreservesEarlierFieldsAndExplicitZero(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			first := usagePresenceWire(protocol, "components", 20, 3, 0, 0)
			last := usagePresenceWire(protocol, "output", 0, 0, 0, 0)
			response, _ := completeUsagePresenceFixture(t, protocol, true, last, first)
			fields := response.Usage.ReportedFields()
			if !fields.Input || !fields.Output || fields.Total || response.Usage.InputTokens != 20 ||
				response.Usage.OutputTokens != 0 || response.Usage.TotalTokens != 20 {
				t.Fatalf("partial final usage reset prior input or lost zero: %+v fields=%+v", response.Usage, fields)
			}
		})
	}
}

func usagePresenceWire(protocol, kind string, input, output, total, cache int) map[string]any {
	if kind == "missing" {
		return nil
	}
	inputKey, outputKey, totalKey, cacheKey := "prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details"
	switch protocol {
	case ProtocolOpenAIResponses:
		inputKey, outputKey, cacheKey = "input_tokens", "output_tokens", "input_tokens_details"
	case ProtocolAnthropic:
		inputKey, outputKey, cacheKey = "input_tokens", "output_tokens", "cache_read_input_tokens"
	case ProtocolGemini:
		inputKey, outputKey, totalKey, cacheKey = "promptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount"
	}
	if kind == "null" {
		return map[string]any{inputKey: nil, outputKey: nil, totalKey: nil}
	}
	wire := map[string]any{outputKey: output}
	if kind != "output" {
		wire[inputKey] = input
	}
	if kind == "complete" {
		if protocol != ProtocolAnthropic {
			wire[totalKey] = total
		}
		if protocol == ProtocolOpenAICompatible || protocol == ProtocolOpenAIResponses {
			wire[cacheKey] = map[string]any{"cached_tokens": cache}
		} else {
			wire[cacheKey] = cache
		}
		if protocol == ProtocolAnthropic {
			wire["cache_creation_input_tokens"] = 0
		}
	}
	return wire
}

func completeUsagePresenceFixture(t *testing.T, protocol string, stream bool, usage, earlier map[string]any) (agentruntime.ModelResponse, AuditRecord) {
	t.Helper()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			writeUsagePresenceStream(t, w, protocol, usage, earlier)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(usagePresenceBody(protocol, usage)); err != nil {
			t.Error(err)
		}
	}))
	defer provider.Close()
	endpoint := provider.URL + "/v1/complete"
	if protocol == ProtocolGemini {
		endpoint = provider.URL + "/v1/models/usage:generateContent"
	}
	var audit AuditRecord
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "usage-presence", Type: "fixture", Protocol: protocol, Endpoint: endpoint},
		Model:    "usage-presence-model", Request: RequestProfile{Timeout: 2 * time.Second, MaxAttempts: 1, MaxResponseBytes: 64 * 1024},
	}, provider.Client(), func(record AuditRecord) { audit = record })
	if err != nil {
		t.Fatal(err)
	}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "inspect a record"}}}
	var response agentruntime.ModelResponse
	if stream {
		response, err = client.(agentruntime.StreamingModelClient).CompleteStream(context.Background(), request, nil)
	} else {
		response, err = client.Complete(context.Background(), request)
	}
	if err != nil || calls != 1 || response.Message.Content != "done" {
		t.Fatalf("legal model response must complete once: calls=%d response=%+v err=%v", calls, response, err)
	}
	return response, audit
}

func usagePresenceBody(protocol string, usage map[string]any) map[string]any {
	body := map[string]any{"id": "usage-request", "usage": usage}
	switch protocol {
	case ProtocolOpenAICompatible:
		body["choices"] = []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}}
	case ProtocolOpenAIResponses:
		body["status"] = "completed"
		body["output"] = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}}}
	case ProtocolAnthropic:
		body["role"], body["stop_reason"] = "assistant", "end_turn"
		body["content"] = []any{map[string]any{"type": "text", "text": "done"}}
	case ProtocolGemini:
		delete(body, "usage")
		body["usageMetadata"] = usage
		body["candidates"] = []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "done"}}}, "finishReason": "STOP"}}
	}
	return body
}

func writeUsagePresenceStream(t *testing.T, w http.ResponseWriter, protocol string, usage, earlier map[string]any) {
	t.Helper()
	emit := func(name string, event any) {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
	}
	switch protocol {
	case ProtocolOpenAICompatible:
		if earlier != nil {
			emit("message", map[string]any{"choices": []any{}, "usage": earlier})
		}
		emit("message", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}}, "usage": usage})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	case ProtocolOpenAIResponses:
		emit("response.completed", map[string]any{"type": "response.completed", "response": usagePresenceBody(protocol, usage)})
	case ProtocolAnthropic:
		startUsage := usage
		if earlier != nil {
			startUsage = earlier
		}
		emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "usage-request", "role": "assistant", "usage": startUsage}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "done"}})
		if earlier != nil {
			emit("message_delta", map[string]any{"type": "message_delta", "usage": usage})
		}
		emit("message_stop", map[string]any{"type": "message_stop"})
	case ProtocolGemini:
		if earlier != nil {
			emit("message", map[string]any{"usageMetadata": earlier})
		}
		emit("message", usagePresenceBody(protocol, usage))
	}
}
