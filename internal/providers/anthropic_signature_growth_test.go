package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestAnthropicSignatureDeltaRecordsOnlyAdmittedBytes(t *testing.T) {
	const frame = `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-fixture"}}`
	for _, fixture := range []struct {
		name, data   string
		observations int
		bytes        int64
	}{
		{"signature_only", frame, 1, int64(len("opaque-fixture"))},
		// These are incremental protocol fragments, not identity snapshots.
		{"equal_fragments", frame + "\n\ndata: " + frame, 2, int64(2 * len("opaque-fixture"))},
		{"empty", strings.Replace(frame, "opaque-fixture", "", 1), 0, 0},
		{"unknown_delta", strings.Replace(frame, "signature_delta", "unknown_fixture", 1), 0, 0},
		{"foreign_protocol", `{"candidates":[{"content":{"parts":[{"signature":"opaque-fixture"}]}}]}`, 0, 0},
		{"invalid_shape", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":{}}}`, 0, 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: 4096}}}
			callbacks := 0
			response, _, _, emitted, err := readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, func(agentruntime.ModelStreamEvent) error {
				callbacks++
				return nil
			})
			if err == nil || reader.observations != fixture.observations || reader.bytes != fixture.bytes || callbacks != 0 || emitted || response.Message.Content != "" || response.Message.ReasoningContent != "" || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("signature admission/privacy changed: err=%v observations=%d growth=%d callbacks=%d emitted=%t", err, reader.observations, reader.bytes, callbacks, emitted)
			}
		})
	}
}

func TestAnthropicSignatureDeltaKeepsSemanticBudget(t *testing.T) {
	const limit = int64(512)
	frame := "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + strings.Repeat("x", 64) + "\"}}\n\n"
	reader := &generationProgressReader{Reader: strings.NewReader(strings.Repeat(frame, 20))}
	client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: limit}}}
	callbacks := 0
	_, _, _, emitted, err := readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, func(agentruntime.ModelStreamEvent) error {
		callbacks++
		return nil
	})
	if !errors.Is(err, errProviderResponseTooLarge) || reader.bytes != limit || reader.observations != 8 || callbacks != 0 || emitted {
		t.Fatalf("signature escaped native budget/privacy: err=%v growth=%d observations=%d callbacks=%d emitted=%t", err, reader.bytes, reader.observations, callbacks, emitted)
	}
}

func TestAnthropicSignatureDeltaCompletesAfterInitialWindow(t *testing.T) {
	for _, ending := range []string{"content", "tool"} {
		t.Run(ending, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"role\":\"assistant\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n")
				ticker := time.NewTicker(15 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; i < 12; i++ {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
					fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-fixture-fragment\"}}\n\n")
					w.(http.Flusher).Flush()
				}
				fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
				if ending == "content" {
					fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"ready\"}}\n\n")
				} else {
					fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\",\"name\":\"lookup\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n")
				}
				fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
			}))
			defer api.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "signature-fixture", Protocol: ProtocolAnthropic, Endpoint: api.URL},
				Model:    "fixture", Request: RequestProfile{Timeout: 60 * time.Millisecond, MaxAttempts: 1, MaxResponseBytes: 4096},
			}, api.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var public strings.Builder
			privateCallbacks := 0
			response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Tools: []agentruntime.ToolSchema{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error {
				public.WriteString(event.ContentDelta)
				if event.ReasoningActive {
					privateCallbacks++
				}
				return nil
			})
			if err != nil || ctx.Err() != nil || privateCallbacks != 0 || response.Message.ReasoningContent != "" {
				t.Fatalf("signature activity did not finish privately: err=%v parent=%v privateCallbacks=%d", err, ctx.Err(), privateCallbacks)
			}
			if ending == "content" && (public.String() != "ready" || response.Message.Content != "ready") {
				t.Fatal("signature activity lost subsequent public content")
			}
			if ending == "tool" && (public.Len() != 0 || len(response.Message.ToolCalls) != 1 || string(response.Message.ToolCalls[0].Arguments) != "{}") {
				t.Fatal("signature activity lost the complete subsequent tool proposal")
			}
		})
	}
}

func TestAnthropicSignatureDeltaPreservesCallerCancellation(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-fixture\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer api.Close()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "signature-cancel-fixture", Protocol: ProtocolAnthropic, Endpoint: api.URL},
		Model:    "fixture", Request: RequestProfile{Timeout: time.Second, MaxAttempts: 1, MaxResponseBytes: 4096},
	}, api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "lookup"}},
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || IsProviderGenerationNoAction(err) || IsProviderPlanningNoProgress(err) {
		t.Fatalf("signature growth relabeled caller cancellation: %v", err)
	}
}
