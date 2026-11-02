package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestProviderResponseDiagnosticsActualHTTP(t *testing.T) {
	tool := `{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}`
	tests := []struct {
		name, contentType, body string
		stream                  bool
		calls                   int
		wantError               bool
		want                    map[string]any
	}{
		{"json text", "application/json", `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, false, 0, false,
			diagnosticWant("stop", true, false, 0, 0, "decoded", false)},
		{"json empty choice", "application/json", `{"choices":[{"message":{},"finish_reason":"stop"}]}`, false, 0, false,
			diagnosticWant("stop", true, false, 0, 0, "empty_response", false)},
		{"json tool", "application/json", `{"choices":[{"message":{"role":"assistant","tool_calls":[` + tool + `]},"finish_reason":"tool_calls"}]}`, false, 1, false,
			diagnosticWant("tool_calls", true, true, 1, 1, "decoded", false)},
		{"stream json fallback", "application/json", `{"choices":[{"message":{"role":"assistant","tool_calls":[` + tool + `]},"finish_reason":"tool_calls"}]}`, true, 1, false,
			diagnosticWant("tool_calls", true, true, 1, 1, "decoded", false)},
		{"json length", "application/json", `{"choices":[{"message":{"role":"assistant","tool_calls":[` + tool + `]},"finish_reason":"length"}]}`, false, 0, true,
			diagnosticWant("length", true, true, nil, 0, "output_limited", true)},
		{"json malformed", "application/json", `{"choices":`, false, 0, true,
			diagnosticWant(nil, nil, nil, nil, 0, "invalid_json", nil)},
		{"http200 error", "application/json", `{"error":{"message":"private-provider-message"}}`, false, 0, true,
			diagnosticWant(nil, nil, nil, nil, 0, "provider_error_envelope", nil)},
		{"json argument feedback", "application/json", `{"choices":[{"message":{"role":"assistant","content":"progress","tool_calls":[{"id":"call-1","function":{"name":"lookup","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`, false, 1, false,
			diagnosticWant("tool_calls", true, true, 1, 1, "protocol_feedback", false)},
		{"sse fragments", "text/event-stream", diagnosticSSE(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"q\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`, "[DONE]"), true, 1, false,
			diagnosticWant("tool_calls", true, true, 1, 1, "decoded", false)},
		{"sse done only", "text/event-stream", diagnosticSSE(`{"choices":[{"delta":{"content":"ok"}}]}`, "[DONE]"), true, 0, false,
			diagnosticWant(nil, true, false, 0, 0, "decoded", false)},
		{"sse cumulative", "text/event-stream", diagnosticSSE(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
			`{"choices":[{"finish_reason":"tool_calls","delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":1}"}}]}}]}`, "[DONE]"), true, 1, false,
			diagnosticWant("tool_calls", true, true, 1, 1, "decoded", false)},
		{"sse interleaved", "text/event-stream", diagnosticSSE(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{"}},{"index":1,"id":"call-2","function":{"name":"save","arguments":"{"}}]}}]}`,
			`{"choices":[{"finish_reason":"tool_calls","delta":{"tool_calls":[{"index":1,"function":{"arguments":"}"}},{"index":0,"function":{"arguments":"}"}}]}}]}`, "[DONE]"), true, 2, false,
			diagnosticWant("tool_calls", true, true, 2, 2, "decoded", false)},
		{"sse length", "text/event-stream", diagnosticSSE(`{"choices":[{"finish_reason":"length","delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{"}}]}}]}`, "[DONE]"), true, 0, true,
			diagnosticWant("length", true, true, nil, 0, "output_limited", true)},
		{"sse partial eof", "text/event-stream", diagnosticSSE(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{"}}]}}]}`), true, 0, true,
			diagnosticWant(nil, false, true, nil, 0, "incomplete_stream", true)},
		{"sse malformed", "text/event-stream", diagnosticSSE(`{"choices":`), true, 0, true,
			diagnosticWant(nil, false, nil, nil, 0, "invalid_json", true)},
		{"sse empty", "text/event-stream", diagnosticSSE("[DONE]"), true, 0, true,
			diagnosticWant(nil, true, nil, nil, 0, "empty_response", false)},
		{"sse http200 error", "text/event-stream", diagnosticSSE(`{"error":{"message":"private-provider-message"}}`, "[DONE]"), true, 0, true,
			diagnosticWant(nil, true, nil, nil, 0, "provider_error_envelope", false)},
		{"sse unsupported reason", "text/event-stream", diagnosticSSE(`{"choices":[{"finish_reason":"private-provider-reason","delta":{}}]}`, "[DONE]"), true, 0, true,
			diagnosticWant("unsupported", false, nil, nil, 0, "invalid_protocol", true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			audits := make(chan AuditRecord, 2)
			client := diagnosticClient(t, server, func(record AuditRecord) { audits <- record })
			var response agentruntime.ModelResponse
			var err error
			if test.stream {
				response, err = client.(agentruntime.StreamingModelClient).CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil)
			} else {
				response, err = client.Complete(context.Background(), agentruntime.ModelRequest{})
			}
			if (err != nil) != test.wantError || len(response.Message.ToolCalls) != test.calls {
				t.Fatalf("response calls=%d err=%v", len(response.Message.ToolCalls), err)
			}
			if requests.Load() != 1 || len(audits) != 1 {
				t.Fatalf("requests=%d audits=%d; diagnostics changed transport", requests.Load(), len(audits))
			}
			record := <-audits
			if record.HTTPStatus != 200 {
				t.Fatalf("status=%d", record.HTTPStatus)
			}
			got := diagnosticJSON(t, record)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("diagnostics=%#v want=%#v", got, test.want)
			}
			raw, _ := json.Marshal(record)
			if strings.Contains(string(raw), "private-provider-") {
				t.Fatalf("diagnostic leaked provider text: %s", raw)
			}
		})
	}
}

func TestProviderResponseDiagnosticsFinalContentOutputLimit(t *testing.T) {
	for _, reason := range []string{"max_tokens", "max_output_tokens"} {
		t.Run(reason, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"prefix"},"finish_reason":"` + reason + `"}]}`))
			}))
			defer server.Close()
			audits := make(chan AuditRecord, 1)
			client := diagnosticClient(t, server, func(record AuditRecord) { audits <- record })
			response, err := client.Complete(context.Background(), agentruntime.ModelRequest{})
			if !IsProviderOutputTokenLimit(err) || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("original final-attempt result changed: response=%#v err=%v", response, err)
			}
			if requests.Load() != 1 || len(audits) != 1 {
				t.Fatalf("requests=%d audits=%d", requests.Load(), len(audits))
			}
			got := diagnosticJSON(t, <-audits)
			want := diagnosticWant("unsupported", true, false, 0, 0, "output_limited", true)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("final typed limit diagnostics=%#v want=%#v", got, want)
			}
		})
	}
}

func TestProviderResponseDiagnosticsCancelBeforeDecode(t *testing.T) {
	accepted := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(accepted)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	audits := make(chan AuditRecord, 1)
	client := diagnosticClient(t, server, func(record AuditRecord) { audits <- record })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{}, nil)
		done <- err
	}()
	<-accepted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if got := diagnosticJSON(t, <-audits); !reflect.DeepEqual(got, diagnosticWant(nil, nil, nil, nil, nil, "canceled", nil)) {
		t.Fatalf("unobserved decoder became zero: %#v", got)
	}
}

func TestProviderResponseDiagnosticsUnwiredResponsesRemainUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"response-other","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()
	audits := make(chan AuditRecord, 1)
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "other", Protocol: ProtocolOpenAIResponses, Endpoint: server.URL},
		Model:    "synthetic", Request: RequestProfile{MaxAttempts: 1, Timeout: time.Second},
	}, server.Client(), func(record AuditRecord) { audits <- record })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), agentruntime.ModelRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := diagnosticJSON(t, <-audits); got != nil {
		t.Fatalf("uninstrumented protocol acquired raw facts: %#v", got)
	}
}

func TestProviderResponseDiagnosticsConsumerAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumer", true: "canceled"}[canceled], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(diagnosticSSE(`{"choices":[{"delta":{"content":"prefix"}}]}`, "[DONE]")))
			}))
			defer server.Close()
			audits := make(chan AuditRecord, 2)
			client := diagnosticClient(t, server, func(record AuditRecord) { audits <- record })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("fixture consumer rejected output")
			outcome := "consumer_error"
			if canceled {
				cause, outcome = context.Canceled, "canceled"
			}
			_, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{}, func(agentruntime.ModelStreamEvent) error {
				if canceled {
					cancel()
				}
				return cause
			})
			if !errors.Is(err, cause) || len(audits) != 1 {
				t.Fatalf("err=%v audits=%d", err, len(audits))
			}
			if got := diagnosticJSON(t, <-audits); !reflect.DeepEqual(got, diagnosticWant(nil, false, nil, nil, 0, outcome, true)) {
				t.Fatalf("diagnostics=%#v", got)
			}
		})
	}
}

func TestProviderResponseDiagnosticsConcurrentRequestIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Fixture") == "tool" {
			_, _ = w.Write([]byte(`{"id":"response-tool","choices":[{"message":{"tool_calls":[{"id":"call-1","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"response-text","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
		}
	}))
	defer server.Close()
	audits := make(chan AuditRecord, 2)
	client := diagnosticClient(t, server, func(record AuditRecord) { audits <- record })
	done := make(chan error, 2)
	for _, kind := range []string{"text", "tool"} {
		go func() {
			_, err := client.Complete(context.Background(), agentruntime.ModelRequest{Headers: map[string]string{"X-Fixture": kind}})
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		record := <-audits
		count := float64(0)
		if record.RequestID == "response-tool" {
			count = 1
		}
		if got := diagnosticJSON(t, record); got["rawToolCallCount"] != count || got["decodedToolCallCount"] != count {
			t.Fatalf("request=%s diagnostics=%#v", record.RequestID, got)
		}
	}
}

func diagnosticClient(t *testing.T, server *httptest.Server, audit func(AuditRecord)) agentruntime.ModelClient {
	t.Helper()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "synthetic", Protocol: ProtocolOpenAICompatible, Endpoint: server.URL},
		Model:    "synthetic", Request: RequestProfile{Timeout: 2 * time.Second, MaxAttempts: 1, MaxResponseBytes: 65536},
	}, server.Client(), audit)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func diagnosticSSE(events ...string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

func diagnosticWant(reason, terminal, observed, rawCount, decoded any, outcome string, incomplete any) map[string]any {
	want := map[string]any{}
	for key, value := range map[string]any{"terminationReason": reason, "terminalObserved": terminal, "rawToolCallsObserved": observed, "rawToolCallCount": rawCount, "decodedToolCallCount": decoded, "outcome": outcome, "incomplete": incomplete} {
		if value != nil {
			if number, ok := value.(int); ok {
				value = float64(number)
			}
			want[key] = value
		}
	}
	return want
}

func diagnosticJSON(t *testing.T, record AuditRecord) map[string]any {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	diagnostic, _ := value["responseDiagnostics"].(map[string]any)
	return diagnostic
}
