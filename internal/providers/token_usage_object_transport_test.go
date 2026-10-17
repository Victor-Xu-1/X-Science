package providers

import (
	"bytes"
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

type usageObjectReceipt struct {
	present bool
	value   any
}

const usageObjectArguments = `{"term":"record"}`

func usageObjectBody(protocol string, receipt usageObjectReceipt, badPrimary bool) map[string]any {
	body := usagePresenceBody(protocol, nil)
	key := "usage"
	body["id"], body["model"] = "usage-object", "usage-object-model"
	switch protocol {
	case ProtocolOpenAICompatible:
		body["choices"] = []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": "done", "tool_calls": []any{map[string]any{
				"id": "call-usage", "type": "function", "function": map[string]any{"name": "lookup", "arguments": usageObjectArguments},
			}}}, "finish_reason": "tool_calls",
		}}
	case ProtocolOpenAIResponses:
		body["output"] = append(body["output"].([]any), map[string]any{
			"type": "function_call", "id": "item-usage", "call_id": "call-usage", "name": "lookup", "arguments": usageObjectArguments,
		})
	case ProtocolAnthropic:
		body["content"] = append(body["content"].([]any), map[string]any{
			"type": "tool_use", "id": "call-usage", "name": "lookup", "input": map[string]any{"term": "record"},
		})
		body["stop_reason"] = "tool_use"
	case ProtocolGemini:
		key = "usageMetadata"
		body["responseId"], body["modelVersion"] = "usage-object", "usage-object-model"
		content := body["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)
		content["parts"] = append(content["parts"].([]any), map[string]any{"functionCall": map[string]any{
			"name": "lookup", "args": map[string]any{"term": "record"},
		}})
	}
	if receipt.present {
		body[key] = receipt.value
	} else {
		delete(body, key) // True schema absence, not an interface holding a nil map.
	}
	if badPrimary {
		key := "id"
		if protocol == ProtocolGemini {
			key = "responseId"
		}
		body[key] = 17
	}
	return body
}

func usageObjectStream(t *testing.T, protocol string, receipt usageObjectReceipt, badPrimary bool) string {
	t.Helper()
	body := usageObjectBody(protocol, receipt, badPrimary)
	var payload strings.Builder
	emit := func(name string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&payload, "event: %s\ndata: %s\n\n", name, raw)
	}
	withUsage := func(event map[string]any) map[string]any {
		if receipt.present {
			event["usage"] = receipt.value
		}
		return event
	}
	switch protocol {
	case ProtocolOpenAICompatible:
		emit("message", map[string]any{"id": "usage-object", "choices": []any{map[string]any{"index": 0,
			"delta": map[string]any{"role": "assistant", "content": "done"},
		}}})
		emit("message", map[string]any{"id": "usage-object", "choices": []any{map[string]any{"index": 0,
			"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call-usage", "type": "function",
				"function": map[string]any{"name": "lookup", "arguments": usageObjectArguments}}}},
			"finish_reason": "tool_calls",
		}}})
		emit("message", withUsage(map[string]any{"id": body["id"], "choices": []any{}}))
		payload.WriteString("data: [DONE]\n\n")
	case ProtocolOpenAIResponses:
		item := body["output"].([]any)[1]
		emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": "done"})
		emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": item})
		emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta",
			"item_id": "item-usage", "delta": usageObjectArguments})
		emit("response.completed", map[string]any{"type": "response.completed", "response": body})
	case ProtocolAnthropic:
		emit("message_start", map[string]any{"type": "message_start", "message": withUsage(map[string]any{"id": body["id"], "role": "assistant"})})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "done"}})
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 1,
			"content_block": map[string]any{"type": "tool_use", "id": "call-usage", "name": "lookup"}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": usageObjectArguments}})
		emit("message_stop", map[string]any{"type": "message_stop"})
	case ProtocolGemini:
		emit("message", body)
	}
	return payload.String()
}

func TestProviderUsageObjectsPreservePrimaryAcrossJSONAndSSE(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"container", "details", "absent", "null", "primary"} {
				if mode == "details" && protocol != ProtocolOpenAICompatible && protocol != ProtocolOpenAIResponses {
					continue
				}
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, stream, mode), func(t *testing.T) {
					receipt := usageObjectReceipt{present: true, value: []any{"private-usage-junk"}}
					wantInvalid := usageObjectFields(protocol)
					wantReported := agentruntime.ModelUsageFields{}
					if mode == "absent" || mode == "null" {
						receipt = usageObjectReceipt{present: mode == "null"}
						wantInvalid = agentruntime.ModelUsageFields{}
					}
					if mode == "details" {
						wire := usagePresenceWire(protocol, "complete", 20, 3, 23, 5)
						key := "prompt_tokens_details"
						if protocol == ProtocolOpenAIResponses {
							key = "input_tokens_details"
						}
						wire[key] = "private-usage-junk"
						receipt.value = wire
						wantInvalid = agentruntime.ModelUsageFields{CacheRead: true}
						wantReported = agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}
					}
					payload, err := json.Marshal(usageObjectBody(protocol, receipt, mode == "primary"))
					if err != nil {
						t.Fatal(err)
					}
					if stream {
						payload = []byte(usageObjectStream(t, protocol, receipt, mode == "primary"))
					}
					var calls atomic.Int32
					audits := []AuditRecord{}
					provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						contentType := "application/json"
						if stream {
							contentType = "text/event-stream"
						}
						w.Header().Set("Content-Type", contentType)
						_, _ = w.Write(payload)
					}))
					defer provider.Close()
					endpoint := provider.URL
					if protocol == ProtocolGemini {
						endpoint += "/models/fixture:generateContent"
					}
					attempts := 2
					if mode == "primary" {
						attempts = 1 // Do not alter the existing retry policy for primary protocol errors.
					}
					client, err := NewRuntimeModelClient(ModelProfile{
						Provider: ProviderProfile{ID: "usage-object", Type: "fixture", Protocol: protocol, Endpoint: endpoint},
						Model:    "usage-object-model", Request: RequestProfile{Timeout: 2 * time.Second, MaxAttempts: attempts, MaxResponseBytes: 64 << 10},
					}, provider.Client(), func(record AuditRecord) { audits = append(audits, record) })
					if err != nil {
						t.Fatal(err)
					}
					request := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}}
					var response agentruntime.ModelResponse
					if stream {
						response, err = client.(agentruntime.StreamingModelClient).CompleteStream(t.Context(), request, nil)
					} else {
						response, err = client.Complete(t.Context(), request)
					}
					if calls.Load() != 1 || len(audits) != 1 {
						t.Fatalf("usage triggered extra requests/audits: calls=%d audits=%d", calls.Load(), len(audits))
					}
					if mode == "primary" {
						var typeError *json.UnmarshalTypeError
						if !errors.As(err, &typeError) || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 || audits[0].Error == "" {
							t.Fatalf("bad primary envelope was partially consumed: err=%v", err)
						}
						return
					}
					if err != nil || response.Message.Content != "done" || response.Message.Role != "assistant" ||
						response.RequestID != "usage-object" || response.Model != "usage-object-model" || len(response.Message.ToolCalls) != 1 || audits[0].Error != "" {
						t.Fatalf("valid primary result changed: response=%+v err=%v", response, err)
					}
					wantID := "call-usage"
					if protocol == ProtocolGemini {
						wantID = "gemini_call_1"
					}
					call := response.Message.ToolCalls[0]
					if call.ID != wantID || call.Name != "lookup" || string(call.Arguments) != usageObjectArguments {
						t.Fatalf("tool identity/arguments changed: %+v", call)
					}
					wantStop := ""
					switch protocol {
					case ProtocolOpenAICompatible:
						wantStop = "tool_calls"
					case ProtocolOpenAIResponses:
						wantStop = "completed"
					case ProtocolAnthropic:
						if !stream {
							wantStop = "tool_use"
						}
					case ProtocolGemini:
						if !stream {
							wantStop = "STOP"
						}
					}
					if response.StopReason != wantStop {
						t.Fatalf("usage changed the transport's existing terminal contract: %q want=%q", response.StopReason, wantStop)
					}
					usage := response.Usage
					if usage.InvalidFields != wantInvalid || usage.ReportedFields() != wantReported ||
						usage.Warnings != audits[0].UsageWarnings || usage.Warnings.Empty() != (wantInvalid == (agentruntime.ModelUsageFields{})) {
						t.Fatalf("response/audit telemetry diverged: usage=%+v audit=%+v", usage, audits[0])
					}
					if mode == "details" && (usage.InputTokens != 20 || usage.OutputTokens != 3 || usage.TotalTokens != 23) {
						t.Fatal("bad details erased valid base counters")
					}
					raw, err := json.Marshal(struct {
						Usage agentruntime.ModelUsage
						Audit AuditRecord
					}{usage, audits[0]})
					if err != nil || bytes.Contains(raw, []byte("private-usage-junk")) {
						t.Fatal("diagnostic retained raw usage junk")
					}
				})
			}
		}
	}
}
