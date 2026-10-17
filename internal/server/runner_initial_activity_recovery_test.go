package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/providers"
	"x-science/internal/sessionrunner"
)

func TestGenerationRecoveryInitialActivityDeadlineUsesDurableBoundedRecovery(t *testing.T) {
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer api.Close()
	client, err := providers.NewRuntimeModelClient(providers.ModelProfile{
		Provider: providers.ProviderProfile{ID: "quiet-response", Protocol: providers.ProtocolOpenAICompatible, Endpoint: api.URL},
		Model:    "fixture", Request: providers.RequestProfile{Timeout: 30 * time.Millisecond, MaxAttempts: 1},
	}, api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, cause := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "lookup"}},
	}, nil)
	if !providers.IsProviderPlanningNoNativeProgress(cause) || ctx.Err() != nil || requests.Load() != 1 ||
		response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
		t.Fatalf("fixture did not produce one native quiet-response boundary: cause=%v requests=%d", cause, requests.Load())
	}

	fixture := newNoProgressReceiptFixture(t)
	for cycle := 1; cycle <= sessionrunner.MaxRecoveryStreak; cycle++ {
		park, err := fixture.server.checkpointGenerationRecovery(context.Background(), fixture.run, cause)
		if err != nil || park != (cycle == sessionrunner.MaxRecoveryStreak) {
			t.Fatalf("initial-activity recovery skipped the existing bounded policy: cycle=%d park=%t err=%v", cycle, park, err)
		}
		state := fixture.run.generationRecoverySnapshot()
		if state.Consecutive != cycle || state.NotBefore.IsZero() != park {
			t.Fatalf("recovery count or durable backoff mismatch: cycle=%d state=%+v park=%t", cycle, state, park)
		}
		if cycle == 1 {
			// Restore from the canonical journal, not an in-memory retry loop.
			resumed := &sessionRunnerChatRun{SessionID: fixture.run.SessionID, Transcript: fixture.run.Transcript}
			if _, err := fixture.server.loadTranscriptRunnerReplay(context.Background(), fixture.run.Transcript, 1, 1, resumed); err != nil {
				t.Fatal(err)
			}
			if restored := resumed.generationRecoverySnapshot(); restored != state {
				t.Fatalf("reclaim lost the finite streak or backoff: got=%+v want=%+v", restored, state)
			}
			fixture.run = resumed
		}
	}
	// Repeated claims cannot reset the exhausted route; they must remain bounded.
	if park, err := fixture.server.checkpointGenerationRecovery(context.Background(), fixture.run, cause); err != nil || !park ||
		fixture.run.generationRecoverySnapshot().Consecutive != sessionrunner.MaxRecoveryStreak {
		t.Fatalf("exhausted route reopened: park=%t err=%v", park, err)
	}
	// No provider-level replay or partial tool action was introduced by recovery.
	if requests.Load() != 1 {
		t.Fatalf("checkpoint recovery replayed the provider: %d", requests.Load())
	}
}
