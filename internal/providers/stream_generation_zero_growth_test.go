package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestProviderUnchangedToolSnapshotCannotRenewIdleOrExecute(t *testing.T) {
	const snapshot = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x"}}]}}]}`
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", snapshot)
		w.(http.Flusher).Flush()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprintf(w, "data: %s\n\n", snapshot)
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer api.Close()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "unchanged-snapshot", Protocol: ProtocolOpenAICompatible, Endpoint: api.URL},
		Model:    "fixture", Request: RequestProfile{Timeout: 60 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 4096},
	}, api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	boundaries := 0
	response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "lookup"}},
	}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
			boundaries++
		}
		return nil
	})
	if !IsProviderGenerationNoAction(err) || !errors.Is(err, errOpenAIChatStreamIdleTimeout) || IsProviderGenerationNoNativeAction(err) ||
		requests.Load() != 1 || boundaries != 1 || ctx.Err() != nil || len(response.Message.ToolCalls) != 0 {
		t.Fatalf("unchanged incomplete tool became progress/action: err=%v parent=%v requests=%d boundaries=%d calls=%v", err, ctx.Err(), requests.Load(), boundaries, response.Message.ToolCalls)
	}
}

func TestProviderGenerationNoActionIdentityRejectsUntrustedCauses(t *testing.T) {
	for _, err := range []error{
		errors.New("provider generation reached its idle or response-byte boundary without completing a public response or tool action"),
		errOpenAIChatStreamIdleTimeout,
		newProviderSemanticSegmentBoundary(16),
		context.Canceled,
		context.DeadlineExceeded,
	} {
		if IsProviderGenerationNoAction(err) || IsProviderGenerationNoNativeAction(err) {
			t.Fatalf("untrusted error acquired decoder proof: %v", err)
		}
	}
	var native atomic.Bool
	valid := newProviderGenerationNoAction(errOpenAIChatStreamIdleTimeout, &native)
	if !IsProviderGenerationNoNativeAction(valid) || !errors.Is(valid, errOpenAIChatStreamIdleTimeout) {
		t.Fatalf("real proof lost: %v", valid)
	}
	native.Store(true)
	if IsProviderGenerationNoNativeAction(valid) {
		t.Fatal("racing native boundary was discarded")
	}
}

func TestDecoderGrowthNotPrivateCallbackOwnsInitialWindow(t *testing.T) {
	ctx, cancel, emit, planning := toolPlanningStreamContext(context.Background(), agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "fixture"}},
	}, 20*time.Millisecond, nil)
	defer cancel(nil)
	defer planning.Stop()
	if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true}); err != nil {
		t.Fatal(err)
	}
	planning.ObserveGrowth(0)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("callback or zero growth prolonged initial generation")
	}
	if !IsProviderPlanningNoProgress(context.Cause(ctx)) {
		t.Fatalf("initial cause=%v", context.Cause(ctx))
	}
	if IsProviderGenerationNoAction(planning.NoCompletedAction(errOpenAIChatStreamIdleTimeout)) {
		t.Fatal("callback invented admitted activity")
	}
}

func TestProviderPrivateGrowthDoesNotRelabelCallerCancellation(t *testing.T) {
	for _, fixture := range []struct{ protocol, initial, private string }{
		{ProtocolOpenAICompatible, "", `{"choices":[{"delta":{"reasoning_content":"private fixture"}}]}`},
		{ProtocolAnthropic, `{"type":"message_start","message":{"id":"msg","role":"assistant"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private fixture"}}`},
		{ProtocolGemini, "", `{"candidates":[{"content":{"parts":[{"text":"private fixture","thought":true}]}}]}`},
		{ProtocolOpenAIResponses, "", `{"type":"response.reasoning_summary_text.delta","delta":"private fixture"}`},
	} {
		t.Run(fixture.protocol, func(t *testing.T) {
			var requests atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if fixture.initial != "" {
					fmt.Fprintf(w, "data: %s\n\n", fixture.initial)
				}
				fmt.Fprintf(w, "data: %s\n\n", fixture.private)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer api.Close()
			endpoint := api.URL
			if fixture.protocol == ProtocolGemini {
				endpoint += "/v1beta/models/model:generateContent"
			}
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "private-cancellation", Protocol: fixture.protocol, Endpoint: endpoint},
				Model:    "fixture", Request: RequestProfile{Timeout: 500 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 4096},
			}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Tools: []agentruntime.ToolSchema{{Name: "fixture"}},
			}, func(event agentruntime.ModelStreamEvent) error {
				if event.ReasoningActive {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || IsProviderGenerationNoAction(err) || IsProviderPlanningNoProgress(err) ||
				IsProviderEmptyResponse(err) || requests.Load() != 1 || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("caller cancellation became generation recovery: err=%v requests=%d response=%+v", err, requests.Load(), response)
			}
		})
	}
}
