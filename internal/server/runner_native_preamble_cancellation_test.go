package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativePublicPreambleCancellationPreservesOnlyAcceptedPrefix(t *testing.T) {
	const preamble = "I will inspect the stored fixture value."
	for _, afterPrefix := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-boundary", true: "after-prefix"}[afterPrefix], func(t *testing.T) {
			var calls atomic.Int64
			bodySent, proposalSent := make(chan struct{}), make(chan struct{})
			releaseBody := make(chan struct{})
			var releaseOnce sync.Once
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&map[string]any{}); err != nil {
					t.Error(err)
					return
				}
				if calls.Add(1) != 1 {
					t.Error("cancelled runner issued another provider request")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writePublicationFixtureEvent(w, map[string]any{"role": "assistant", "content": preamble}, "")
				close(bodySent)
				select {
				case <-releaseBody:
				case <-r.Context().Done():
					return
				}
				writeNativePreambleTool(w, `{"namespace":"fixture"`, "")
				close(proposalSent)
				<-r.Context().Done() // No full arguments, action or final exists after cancellation.
			}))
			t.Cleanup(provider.Close)
			server, repo, options := newNativePreambleFixture(t, provider.URL, "cancel-preamble-frame")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(releaseBody) })
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("cancelled owned runner did not join")
				}
			})
			go func() {
				_, _ = server.RunSessionRunnerChatOnce(ctx, options)
				close(done)
			}()
			select {
			case <-bodySent:
			case <-time.After(5 * time.Second):
				t.Fatal("provider did not reach body barrier")
			}
			want := ""
			if afterPrefix {
				releaseOnce.Do(func() { close(releaseBody) })
				select {
				case <-proposalSent:
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not reach partial-proposal barrier")
				}
				want = preamble
				deadline := time.Now().Add(time.Second)
				for {
					progress, _ := publicationFixtureText(t, repo, options.SessionID)
					if progress == want {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("positive prefix did not reach canonical persistence before cancellation")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("runner did not settle cancellation")
			}
			progress, final := publicationFixtureText(t, repo, options.SessionID)
			if progress != want || final != "" || calls.Load() != 1 {
				t.Fatalf("cancellation changed accepted prefix: progress=%q final=%q calls=%d", progress, final, calls.Load())
			}
			assertNativePreambleActionCounts(t, repo, options.SessionID, 0, 0)
		})
	}
}
