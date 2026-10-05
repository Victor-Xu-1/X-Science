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

	"synon-go/internal/agentruntime"
)

func TestToolPlanningReasoningOnlyStreamHasBoundedProgress(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolOpenAIResponses, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if protocol == ProtocolAnthropic {
					fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"role\":\"assistant\"}}\n\n")
				}
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; i < 100; i++ {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
					switch protocol {
					case ProtocolAnthropic:
						fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"private\"}}\n\n")
					case ProtocolOpenAIResponses:
						fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"private\"}\n\n")
					case ProtocolGemini:
						fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"private\",\"thought\":true}]}}]}\n\n")
					default:
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
					}
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "bounded-planning", Protocol: protocol, Endpoint: server.URL + "/v1beta/models/model:generateContent"},
				Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
			}, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var visible strings.Builder
			started := time.Now()
			_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Perform the next analysis."}},
				Tools:    []agentruntime.ToolSchema{{Name: "analysis", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error { visible.WriteString(event.ContentDelta); return nil })
			if !IsProviderEmptyResponse(err) || time.Since(started) > time.Second || requests.Load() != 1 {
				t.Fatalf("private-only generation was not bounded once: err=%v elapsed=%s requests=%d", err, time.Since(started), requests.Load())
			}
			if visible.Len() != 0 {
				t.Fatalf("private reasoning leaked: %q", visible.String())
			}
		})
	}
}

func TestToolPlanningUnframedJSONCannotExtendActionProgress(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolOpenAIResponses, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				prefix, suffix := `{"choices":[{"message":{"role":"assistant","reasoning_content":"`, `"},"finish_reason":"stop"}]}`
				switch protocol {
				case ProtocolAnthropic:
					prefix, suffix = `{"id":"msg","role":"assistant","content":[{"type":"thinking","thinking":"`, `"}],"stop_reason":"end_turn"}`
				case ProtocolOpenAIResponses:
					prefix, suffix = `{"id":"resp","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"`, `"}]}]}`
				case ProtocolGemini:
					prefix, suffix = `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"`, `"}]}}]}`
				}
				fmt.Fprint(w, prefix)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; i < 200; i++ {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
					fmt.Fprint(w, "private")
					w.(http.Flusher).Flush()
				}
				fmt.Fprint(w, suffix)
			}))
			defer server.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "unframed-planning", Protocol: protocol, Endpoint: server.URL + "/v1beta/models/model:generateContent"},
				Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
			}, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var visible strings.Builder
			started := time.Now()
			_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Perform the next analysis."}},
				Tools:    []agentruntime.ToolSchema{{Name: "analysis", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error { visible.WriteString(event.ContentDelta); return nil })
			if !IsProviderEmptyResponse(err) || time.Since(started) > time.Second || requests.Load() != 1 || visible.Len() != 0 {
				t.Fatalf("unframed bytes extended action progress: err=%v elapsed=%s requests=%d visible=%q", err, time.Since(started), requests.Load(), visible.String())
			}
		})
	}
}

func TestToolPlanningBeforeHeadersDoesNotTransportReplay(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolOpenAIResponses, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "header-planning", Protocol: protocol, Endpoint: server.URL + "/v1beta/models/model:generateContent"},
				Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
			}, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Perform the next analysis."}},
				Tools:    []agentruntime.ToolSchema{{Name: "analysis", Parameters: map[string]any{"type": "object"}}},
			}, nil)
			if !IsProviderEmptyResponse(err) || requests.Load() != 1 {
				t.Fatalf("first-action expiry replayed before headers: err=%v requests=%d", err, requests.Load())
			}
		})
	}
}

func TestToolPlanningUsefulActionRetainsProductiveStreaming(t *testing.T) {
	for _, action := range []agentruntime.ModelStreamEvent{
		{ContentDelta: "Preparing the requested analysis."},
		{Kind: agentruntime.ModelStreamEventPublicProgressDelta, ContentDelta: "Inputs ready."},
		{Kind: agentruntime.ModelStreamEventToolCallBoundary},
	} {
		ctx, cancel, emit, stop := toolPlanningStreamContext(context.Background(), agentruntime.ModelRequest{
			Tools: []agentruntime.ToolSchema{{Name: "analysis"}},
		}, 20*time.Millisecond, nil)
		if err := emit(agentruntime.ModelStreamEvent{ReasoningActive: true}); err != nil {
			t.Fatal(err)
		}
		if err := emit(action); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("useful action was treated as a private-only stall: %v", context.Cause(ctx))
		case <-time.After(60 * time.Millisecond):
		}
		stop()
		cancel(nil)
	}
}

func TestToolPlanningPreservesCallerCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel, emit, stop := toolPlanningStreamContext(parent, agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "analysis"}},
	}, time.Second, nil)
	defer cancel(nil)
	defer stop()
	if err := emit(agentruntime.ModelStreamEvent{ReasoningActive: true}); err != nil {
		t.Fatal(err)
	}
	cancelParent()
	if context.Cause(ctx) != context.Canceled {
		t.Fatalf("caller cancellation was relabeled: %v", context.Cause(ctx))
	}
}

func TestToolPlanningPrivateStallPreservesAcceptedPrefix(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Accepted prefix.\"}}]}\n\n")
		w.(http.Flusher).Flush()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "prefix-planning", Protocol: ProtocolOpenAICompatible, Endpoint: server.URL},
		Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
	}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var accepted strings.Builder
	_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "Perform an analysis."}},
		Tools:    []agentruntime.ToolSchema{{Name: "analysis", Parameters: map[string]any{"type": "object"}}},
	}, func(event agentruntime.ModelStreamEvent) error { accepted.WriteString(event.ContentDelta); return nil })
	if !IsRecoverableStreamInterruption(err) || IsProviderEmptyResponse(err) || accepted.String() != "Accepted prefix." || requests.Load() != 1 {
		t.Fatalf("prefix was discarded/replayed or private stall persisted: err=%v text=%q requests=%d", err, accepted.String(), requests.Load())
	}
}
