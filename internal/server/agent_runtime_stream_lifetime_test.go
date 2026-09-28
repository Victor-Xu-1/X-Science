package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
)

func TestStaticModelStreamLifetimeUsesProviderProgress(t *testing.T) {
	for _, mode := range []string{"progress", "idle", "caller-cancel", "caller-deadline"} {
		t.Run(mode, func(t *testing.T) {
			const window = 200 * time.Millisecond
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 16; i++ {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"x\"}}]}\n\n")
					w.(http.Flusher).Flush()
					if mode == "idle" {
						<-r.Context().Done()
						return
					}
					select {
					case <-r.Context().Done():
						return
					case <-time.After(30 * time.Millisecond):
					}
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()
			srv := New(Options{FileRoot: t.TempDir()})
			engine := srv.newAgentRuntimeEngine(SessionRunnerChatOptions{
				Endpoint: provider.URL, Model: "stream-test", RequestTimeout: window,
				MaxAttempts: 1, ModelResponseLimitBytes: 64 * 1024,
			})
			streaming, ok := engine.Model.(agentruntime.StreamingModelClient)
			if !ok {
				t.Fatal("deployment model lost streaming interface")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "caller-deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 90*time.Millisecond)
				defer stop()
			}
			var content strings.Builder
			started := time.Now()
			response, err := streaming.CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "stream test"}},
			}, func(event agentruntime.ModelStreamEvent) error {
				content.WriteString(event.ContentDelta)
				if mode == "caller-cancel" && event.ContentDelta != "" {
					cancel()
				}
				return nil
			})
			switch mode {
			case "progress":
				if err != nil || response.Message.Content != strings.Repeat("x", 16) || content.String() != response.Message.Content || time.Since(started) <= 2*window {
					t.Fatalf("healthy stream ended early: elapsed=%s content=%q err=%v", time.Since(started), content.String(), err)
				}
			case "caller-cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("caller cancellation lost: %v", err)
				}
			default:
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) >= time.Second {
					t.Fatalf("bounded timeout lost: elapsed=%s err=%v", time.Since(started), err)
				}
			}
		})
	}
}
