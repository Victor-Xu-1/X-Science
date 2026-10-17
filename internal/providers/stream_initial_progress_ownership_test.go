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

func TestStreamInitialGrowthOwnsDeadlineBeforeSemanticIdle(t *testing.T) {
	_, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	observed := 0
	reader := newOpenAIChatStreamIdleReader(strings.NewReader(""), time.Hour, cancel, func(growth int64) {
		observed += int(growth)
	})
	defer reader.Stop()
	if reader.timer != nil || !reader.awaitingGrowth {
		t.Fatal("idle timer competed with initial growth before any decoded activity")
	}
	reader.recordProgress(0)
	if reader.timer != nil || observed != 0 || !reader.awaitingGrowth {
		t.Fatal("zero-growth metadata acquired the idle window")
	}
	reader.recordProgress(1)
	if reader.timer == nil || reader.awaitingGrowth || observed != 1 {
		t.Fatal("positive decoder growth did not transfer deadline ownership")
	}
	reader.recordProgress(0)
	if reader.timer == nil || observed != 1 {
		t.Fatal("post-growth observation invented decoder growth or lost idle cancellation")
	}
	plain := newOpenAIChatStreamIdleReader(strings.NewReader(""), time.Hour, cancel, nil)
	defer plain.Stop()
	if plain.timer == nil || plain.awaitingGrowth {
		t.Fatal("plain request lost its original transport-idle deadline")
	}
}

func TestStreamQuietResponseKeepsInitialDeadlineAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		for _, format := range []string{"sse", "unframed-json"} {
			t.Run(protocol+"/"+format, func(t *testing.T) {
				var requests atomic.Int32
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						return
					}
					if format == "sse" {
						w.Header().Set("Content-Type", "text/event-stream")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, "{")
					}
					w.(http.Flusher).Flush()
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-r.Context().Done():
							return
						case <-ticker.C:
							if format == "sse" {
								fmt.Fprint(w, ": heartbeat\n\n")
							} else {
								fmt.Fprint(w, " ")
							}
							w.(http.Flusher).Flush()
						}
					}
				}))
				defer api.Close()
				endpoint := api.URL
				if protocol == ProtocolGemini {
					endpoint += "/v1beta/models/model:generateContent"
				}
				client, err := NewRuntimeModelClient(ModelProfile{
					Provider: ProviderProfile{ID: "quiet-generation", Protocol: protocol, Endpoint: endpoint},
					Model:    "fixture", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 4096},
				}, api.Client(), nil)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
					Tools: []agentruntime.ToolSchema{{Name: "lookup"}},
				}, nil)
				if !IsProviderPlanningNoNativeProgress(err) || IsProviderGenerationNoAction(err) ||
					ctx.Err() != nil || requests.Load() != 1 || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
					t.Fatalf("initial activity lost its single recovery authority: err=%v requests=%d", err, requests.Load())
				}
			})
		}
	}
}
