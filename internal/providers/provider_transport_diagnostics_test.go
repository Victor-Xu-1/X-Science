package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

const transportToolStream = "data: {\"id\":\"fixture\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"

func TestProviderTransportStagesInitialDeadline(t *testing.T) {
	for _, fixture := range []struct{ name, format, body string }{
		{"silent body", "sse", ""},
		{"comment body", "sse", ": private-stage-fixture\n\n"},
		{"unframed JSON", "json_fallback", "{"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				contentType := "text/event-stream"
				if fixture.format == "json_fallback" {
					contentType = "application/json"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, fixture.body)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer api.Close()
			audits := make(chan AuditRecord, 2)
			client := transportDiagnosticClient(t, api, 80*time.Millisecond, 3, 4096, func(record AuditRecord) { audits <- record })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, nil)
			if !IsProviderPlanningNoNativeProgress(err) || requests.Load() != 1 || len(audits) != 1 {
				t.Fatalf("existing no-progress/request/audit contract changed: error=%v requests=%d audits=%d", err, requests.Load(), len(audits))
			}
			record := <-audits
			transport, diagnostic := transportAuditJSON(t, record)
			if diagnostic["outcome"] != "empty_response" || record.HTTPStatus != 200 {
				t.Fatalf("original outcome changed: %#v status=%d", diagnostic, record.HTTPStatus)
			}
			if transport["version"] != float64(1) || transport["format"] != fixture.format || transport["stopCause"] != "initial_activity_deadline" ||
				transport["responseBodyBytes"] != float64(len(fixture.body)) || transport["countsSaturated"] != false {
				t.Fatalf("initial no-growth observation is not exact: %#v", transport)
			}
			if fixture.format == "json_fallback" {
				if _, observed := transport["decodedGrowthEvents"]; observed {
					t.Fatalf("JSON decoder never ran, but its growth count was observed: %#v", transport)
				}
			} else if transport["decodedGrowthEvents"] != float64(0) {
				t.Fatalf("observed SSE decoder lost its zero: %#v", transport)
			}
			headerOffset := transportOffset(t, transport, "headersReadyAfterUs")
			if _, exists := transport["firstDecodedGrowthAfterUs"]; exists {
				t.Fatalf("comments became decoded growth: %#v", transport)
			}
			if fixture.body == "" {
				if _, exists := transport["firstBodyReadAfterUs"]; exists {
					t.Fatalf("unobserved body read became zero: %#v", transport)
				}
			} else if transportOffset(t, transport, "firstBodyReadAfterUs") < headerOffset {
				t.Fatalf("body read preceded headers: %#v", transport)
			}
		})
	}
}

func TestProviderTransportHeadersAndParentStopCauses(t *testing.T) {
	for _, stop := range []string{"header_deadline", "parent_canceled", "parent_deadline"} {
		t.Run(stop, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			if stop == "parent_deadline" {
				cancel()
				parent, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
			}
			defer cancel()
			api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if stop == "parent_canceled" {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer api.Close()
			audits := make(chan AuditRecord, 1)
			timeout := time.Second
			if stop == "header_deadline" {
				timeout = 80 * time.Millisecond
			}
			client := transportDiagnosticClient(t, api, timeout, 1, 4096, func(record AuditRecord) { audits <- record })
			_, err := client.CompleteStream(parent, agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, nil)
			if err == nil || len(audits) != 1 {
				t.Fatalf("original stop/audit was lost: %v audits=%d", err, len(audits))
			}
			transport, _ := transportAuditJSON(t, <-audits)
			if transport["stopCause"] != stop {
				t.Fatalf("typed stop=%s observation=%#v", stop, transport)
			}
			for _, missing := range []string{"headersReadyAfterUs", "firstBodyReadAfterUs", "firstDecodedGrowthAfterUs", "responseBodyBytes", "decodedGrowthEvents", "format", "countsSaturated"} {
				if _, found := transport[missing]; found {
					t.Fatalf("unobserved %s was invented: %#v", missing, transport)
				}
			}
		})
	}
}

func TestProviderTransportDecodedStages(t *testing.T) {
	for _, mode := range []string{"private idle", "terminal tool", "json fallback", "plain idle"} {
		t.Run(mode, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "json fallback" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","reasoning_content":"private-stage-fixture","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "terminal tool" {
					_, _ = io.WriteString(w, transportToolStream)
					return
				}
				body := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private-stage-fixture\"}}]}\n\n"
				if mode == "plain idle" {
					body = ": private-stage-fixture\n\n"
				}
				_, _ = io.WriteString(w, body)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer api.Close()
			audits := make(chan AuditRecord, 2)
			client := transportDiagnosticClient(t, api, 80*time.Millisecond, 1, 4096, func(record AuditRecord) { audits <- record })
			request := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}
			if mode == "plain idle" {
				request.Tools = nil
			}
			response, err := client.CompleteStream(context.Background(), request, nil)
			if len(audits) != 1 {
				t.Fatalf("audit count changed: %d", len(audits))
			}
			transport, _ := transportAuditJSON(t, <-audits)
			header := transportOffset(t, transport, "headersReadyAfterUs")
			body := transportOffset(t, transport, "firstBodyReadAfterUs")
			if body < header || transport["responseBodyBytes"].(float64) <= 0 {
				t.Fatalf("invalid observed body stages: %#v", transport)
			}
			if mode == "plain idle" {
				if !errors.Is(err, errOpenAIChatStreamIdleTimeout) || transport["decodedGrowthEvents"] != float64(0) {
					t.Fatalf("plain nil-growth timer changed: error=%v stages=%#v", err, transport)
				}
			} else if transportOffset(t, transport, "firstDecodedGrowthAfterUs") < body || transport["decodedGrowthEvents"] != float64(1) {
				t.Fatalf("decoder growth was not observed exactly: %#v", transport)
			}
			wantStop := "terminal_completed"
			if strings.HasSuffix(mode, "idle") {
				wantStop = "semantic_idle_deadline"
				if err == nil || (mode == "private idle" && !IsProviderGenerationNoAction(err)) {
					t.Fatalf("original semantic-idle outcome changed: %v", err)
				}
			} else if err != nil || len(response.Message.ToolCalls) != 1 {
				t.Fatalf("original terminal tool response changed: %#v %v", response, err)
			}
			wantFormat := "sse"
			if mode == "json fallback" {
				wantFormat = "json_fallback"
			}
			if transport["format"] != wantFormat || transport["stopCause"] != wantStop {
				t.Fatalf("incorrect decoder/stop observation: %#v", transport)
			}
		})
	}
}

func TestProviderTransportFailureAndAttemptIsolation(t *testing.T) {
	for _, mode := range []string{"incomplete_stream", "response_limit", "output_limit", "unknown", "retry"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "retry" && attempt == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "retry fixture")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				body := transportToolStream
				switch mode {
				case "incomplete_stream":
					body = diagnosticSSE(`{"choices":[{"delta":{"reasoning_content":"private-stage-fixture"}}]}`)
				case "response_limit":
					body = "data: {\"padding\":\"" + strings.Repeat("x", 256) + "\"}\n\n"
				case "output_limit":
					body = diagnosticSSE(`{"choices":[{"finish_reason":"length","delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{"}}]}}]}`, "[DONE]")
				case "unknown":
					body = "data: {broken}\n\n"
				}
				_, _ = io.WriteString(w, body)
			}))
			defer api.Close()
			audits := make(chan AuditRecord, 3)
			limit := int64(4096)
			if mode == "response_limit" {
				limit = 64
			}
			client := transportDiagnosticClient(t, api, time.Second, 2, limit, func(record AuditRecord) { audits <- record })
			_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, nil)
			if mode == "retry" {
				if err != nil || requests.Load() != 2 || len(audits) != 2 {
					t.Fatalf("existing HTTP retry changed: error=%v requests=%d audits=%d", err, requests.Load(), len(audits))
				}
				first, _ := transportAuditJSON(t, <-audits)
				second, _ := transportAuditJSON(t, <-audits)
				if first["responseBodyBytes"] != float64(len("retry fixture")) || first["format"] != nil || first["decodedGrowthEvents"] != nil ||
					second["responseBodyBytes"] != float64(len(transportToolStream)) || second["format"] != "sse" || second["decodedGrowthEvents"] != float64(1) {
					t.Fatalf("attempt observations inherited or mutated: first=%#v second=%#v", first, second)
				}
				return
			}
			// Protocol retry remains original; inspect each exact attempt rather
			// than forcing a new diagnostic policy to suppress existing retries.
			if err == nil || len(audits) == 0 || int(requests.Load()) != len(audits) {
				t.Fatalf("existing failure/audit changed: error=%v requests=%d audits=%d", err, requests.Load(), len(audits))
			}
			for len(audits) > 0 {
				transport, _ := transportAuditJSON(t, <-audits)
				if mode == "unknown" {
					if _, exists := transport["stopCause"]; exists {
						t.Fatalf("error text minted a typed cause: %#v", transport)
					}
				} else if transport["stopCause"] != mode {
					t.Fatalf("typed failure %s lost: %#v", mode, transport)
				}
			}
		})
	}
}

func TestProviderTransportNumericBoundsAndFrozenSnapshot(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &providerTransportObservation{started: time.Now(), parent: parent, request: parent,
		bodyBytes: maxTransportDiagnosticInteger - 1, growthEvents: maxTransportDiagnosticInteger - 1}
	body := o.observeHeaders(io.NopCloser(strings.NewReader("ab")))
	o.beginDecode("sse")
	_, _ = io.ReadAll(body)
	o.observeGrowth(1, false)
	o.observeGrowth(1, false)
	value := o.snapshot(nil, diagnosticPointer(true))
	if *value.ResponseBodyBytes != maxTransportDiagnosticInteger || *value.DecodedGrowthEvents != maxTransportDiagnosticInteger || !*value.CountsSaturated {
		t.Fatalf("counter overflow or missing saturation: %#v", value)
	}
	header := *value.HeadersReadyAfterUs
	*o.headers = 99
	o.bodyBytes, o.growthEvents = 0, 0
	if *value.ResponseBodyBytes != maxTransportDiagnosticInteger || *value.DecodedGrowthEvents != maxTransportDiagnosticInteger || *value.HeadersReadyAfterUs != header {
		t.Fatalf("audit snapshot shared mutable observation: %#v", value)
	}
	cancel()
	if cause := o.snapshot(nil, diagnosticPointer(true)).StopCause; cause == nil || *cause != "terminal_completed" {
		t.Fatalf("later cancellation relabeled a completed attempt: %v", cause)
	}
}

func TestProviderTransportUnwiredUnaryRemainsUnknown(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer api.Close()
	var record AuditRecord
	_, err := diagnosticClient(t, api, func(value AuditRecord) { record = value }).Complete(context.Background(), agentruntime.ModelRequest{})
	if err != nil || record.ResponseDiagnostics == nil || record.ResponseDiagnostics.Transport != nil {
		t.Fatalf("unwired unary acquired a transport observation: %#v %v", record.ResponseDiagnostics, err)
	}
}

func transportDiagnosticClient(t *testing.T, api *httptest.Server, timeout time.Duration, attempts int, limit int64, audit func(AuditRecord)) agentruntime.StreamingModelClient {
	t.Helper()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "synthetic", Protocol: ProtocolOpenAICompatible, Endpoint: api.URL},
		Model:    "synthetic", Request: RequestProfile{Timeout: timeout, MaxAttempts: attempts, MaxResponseBytes: limit},
	}, api.Client(), audit)
	if err != nil {
		t.Fatal(err)
	}
	return client.(agentruntime.StreamingModelClient)
}

func transportAuditJSON(t *testing.T, record AuditRecord) (map[string]any, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-stage-fixture") {
		t.Fatal("transport observation retained response content")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	diagnostic, _ := value["responseDiagnostics"].(map[string]any)
	transport, _ := diagnostic["transport"].(map[string]any)
	if transport == nil {
		t.Fatalf("actual HTTP attempt did not retain transport stages: %#v", diagnostic)
	}
	return transport, diagnostic
}

func transportOffset(t *testing.T, value map[string]any, key string) float64 {
	t.Helper()
	number, ok := value[key].(float64)
	if !ok || number < 0 || number > 1<<53-1 {
		t.Fatalf("missing or invalid observed offset %s: %#v", key, value)
	}
	return number
}
