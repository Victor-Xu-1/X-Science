package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestRuntimeModelClientProjectsNativeRefusal(t *testing.T) {
	const refusalText = "Synthetic provider refusal."
	const ordinaryText = "An ordinary answer mentions refusal without refusing."
	cases := []struct {
		name       string
		chat       bool
		stream     bool
		content    map[string]any
		refused    bool
		publicText string
	}{
		{"chat_refusal", true, false, map[string]any{"role": "assistant", "content": refusalText, "refusal": refusalText}, true, refusalText},
		{"chat_ordinary", true, false, map[string]any{"role": "assistant", "content": ordinaryText}, false, ordinaryText},
		{"responses_json_type", false, false, map[string]any{"type": "refusal", "text": refusalText}, true, refusalText},
		{"responses_json_field", false, false, map[string]any{"type": "output_text", "refusal": refusalText}, true, refusalText},
		{"responses_json_ordinary", false, false, map[string]any{"type": "output_text", "text": ordinaryText}, false, ordinaryText},
		{"responses_sse_type", false, true, map[string]any{"type": "refusal", "text": refusalText}, true, refusalText},
		{"responses_sse_field", false, true, map[string]any{"type": "output_text", "refusal": refusalText}, true, refusalText},
		{"responses_sse_ordinary", false, true, map[string]any{"type": "output_text", "text": ordinaryText}, false, ordinaryText},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			stopReason := "completed"
			protocol, endpoint := ProtocolOpenAIResponses, "/v1/responses"
			if test.chat {
				stopReason, protocol, endpoint = "stop", ProtocolOpenAICompatible, "/v1/chat/completions"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != endpoint {
					http.Error(w, "unexpected refusal fixture endpoint", http.StatusBadRequest)
					return
				}
				body := map[string]any{
					"id": "refusal-fixture", "model": "fixture-model", "status": "completed",
					"output": []any{map[string]any{
						"type": "message", "role": "assistant", "content": []any{test.content},
					}},
					"usage": map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
				}
				if test.chat {
					body = map[string]any{
						"id": "refusal-fixture", "model": "fixture-model",
						"choices": []any{map[string]any{"finish_reason": "stop", "message": test.content}},
						"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
					}
				}
				if test.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: response.completed\ndata: "))
					_ = json.NewEncoder(w).Encode(map[string]any{"type": "response.completed", "response": body})
					_, _ = w.Write([]byte("\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(body)
			}))
			t.Cleanup(upstream.Close)
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "refusal-fixture", Protocol: protocol, Endpoint: upstream.URL + endpoint},
				Model:    "fixture-model", Request: RequestProfile{Timeout: time.Second, MaxAttempts: 1, MaxResponseBytes: 64 << 10},
			}, upstream.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Return fixture data."}}}
			var response agentruntime.ModelResponse
			if test.stream {
				response, err = client.(agentruntime.StreamingModelClient).CompleteStream(context.Background(), request, nil)
			} else {
				response, err = client.Complete(context.Background(), request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if response.StopReason != stopReason || response.Message.Content != test.publicText || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("refusal projection changed existing response semantics: %#v", response)
			}
			if response.Usage.InputTokens != 3 || response.Usage.OutputTokens != 2 || response.Usage.TotalTokens != 5 {
				t.Fatalf("refusal projection changed usage: %#v", response.Usage)
			}
			if refused := projectedNativeRefusalForTest(t, response); refused != test.refused {
				t.Fatalf("Refused=%t want=%t", refused, test.refused)
			}
			raw, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var projection map[string]json.RawMessage
			if err := json.Unmarshal(raw, &projection); err != nil {
				t.Fatal(err)
			}
			value, present := projection["refused"]
			if present != test.refused || present && string(value) != "true" {
				t.Fatalf("optional refused metadata=%s present=%t want=%t", value, present, test.refused)
			}
			if _, present := projection["Refused"]; present {
				t.Fatal("default/untagged Refused field changed the old response shape")
			}
		})
	}
}

// Treat absent optional metadata as false so the regression builds against the
// pre-field implementation. Positive cases still require the real native
// decoder to preserve a boolean signal, not a guessed phrase in public text.
func projectedNativeRefusalForTest(t *testing.T, response agentruntime.ModelResponse) bool {
	t.Helper()
	field := reflect.ValueOf(response).FieldByName("Refused")
	if !field.IsValid() {
		return false
	}
	if field.Kind() != reflect.Bool {
		t.Fatalf("ModelResponse.Refused has type %s, want bool", field.Kind())
	}
	return field.Bool()
}
