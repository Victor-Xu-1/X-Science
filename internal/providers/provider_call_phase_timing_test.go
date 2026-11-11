package providers

import (
	"context"
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

func TestProviderCallPhaseTimingTransportFamilies(t *testing.T) {
	fixtures := []struct{ protocol, endpoint, body string }{
		{ProtocolOpenAICompatible, "/v1/chat/completions", "data: {\"id\":\"fixture\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{ProtocolOpenAIResponses, "/v1/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n"},
		{ProtocolAnthropic, "/v1/messages", "data: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"role\":\"assistant\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{ProtocolGemini, "/v1beta/models/fixture:generateContent", "data: {\"responseId\":\"fixture\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.protocol, func(t *testing.T) {
			requests := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "call-phase-private-identity") || r.Header.Get("call-phase-private-identity") != "" {
					t.Error("typed context observation leaked into outbound data")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, ": heartbeat-only\n\n"+fixture.body)
			}))
			defer api.Close()
			client, err := NewRuntimeModelClient(ModelProfile{Provider: ProviderProfile{ID: "fixture", Protocol: fixture.protocol, Endpoint: api.URL + fixture.endpoint},
				Model: "fixture", APIKey: "synthetic-key", Request: RequestProfile{Timeout: time.Second, MaxAttempts: 1, MaxResponseBytes: 65536}}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var attempts []CompletedTransportAttempt
			ctx := WithCompletedTransportObserver(t.Context(), func(value CompletedTransportAttempt) { attempts = append(attempts, value) })
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{}, nil)
			if err != nil || response.Message.Content != "ok" || requests != 1 || len(attempts) != 1 {
				t.Fatalf("delegate/request contract changed: response=%+v err=%v requests=%d attempts=%+v", response, err, requests, attempts)
			}
			d := attempts[0].Diagnostics
			if attempts[0].Attempt != 1 || attempts[0].Protocol != fixture.protocol || d == nil ||
				d.DoEnteredAfterUs == nil || d.HeadersReadyAfterUs == nil || d.FirstBodyReadAfterUs == nil || d.FirstDecodedGrowthAfterUs == nil {
				t.Fatalf("actual transport phases missing: %+v", d)
			}
			prior := int64(0)
			for _, observed := range []*int64{d.DoEnteredAfterUs, d.HeadersReadyAfterUs, d.FirstBodyReadAfterUs, d.FirstDecodedGrowthAfterUs} {
				if *observed < prior {
					t.Fatalf("source phase order changed: %+v", d)
				}
				prior = *observed
			}
		})
	}
}

func TestProviderCallPhaseTimingUnknownAndAttemptIsolation(t *testing.T) {
	t.Run("heartbeat is not decoded growth", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": heartbeat-only\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer api.Close()
		client := transportDiagnosticClient(t, api, 60*time.Millisecond, 1, 4096, nil)
		var observation CompletedTransportAttempt
		ctx := WithCompletedTransportObserver(context.Background(), func(value CompletedTransportAttempt) { observation = value })
		_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, nil)
		if !IsProviderPlanningNoNativeProgress(err) || observation.Diagnostics == nil || observation.Diagnostics.FirstDecodedGrowthAfterUs != nil || observation.Diagnostics.FirstBodyReadAfterUs == nil {
			t.Fatalf("heartbeat became generation progress: %v %+v", err, observation.Diagnostics)
		}
	})
	t.Run("real retry retains two separate attempts", func(t *testing.T) {
		var requests atomic.Int32
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, "temporary")
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, transportToolStream)
		}))
		defer api.Close()
		client := transportDiagnosticClient(t, api, time.Second, 2, 4096, nil)
		var attempts []CompletedTransportAttempt
		ctx := WithCompletedTransportObserver(t.Context(), func(value CompletedTransportAttempt) { attempts = append(attempts, value) })
		_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, nil)
		if err != nil || requests.Load() != 2 || len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 ||
			attempts[0].Diagnostics.FirstDecodedGrowthAfterUs != nil || attempts[1].Diagnostics.FirstDecodedGrowthAfterUs == nil {
			t.Fatalf("retry timing was mixed or retried differently: %v %+v requests=%d", err, attempts, requests.Load())
		}
	})
}
