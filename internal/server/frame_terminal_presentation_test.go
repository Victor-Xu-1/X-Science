package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestCompatibilityTerminalFrameSnapshotDoesNotJoinHistoryCatchup(t *testing.T) {
	fixture := newTerminalPresentationFixture(t)
	before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
	key := transcriptWebCatchupKey{
		ownerID: fixture.stream.OwnerID, streamUID: fixture.stream.UID, branchID: fixture.fence.BranchID,
	}
	flight := &transcriptWebCatchupFlight{done: make(chan struct{})}
	fixture.server.transcriptWebCatchupMu.Lock()
	fixture.server.transcriptWebCatchups = map[transcriptWebCatchupKey]*transcriptWebCatchupFlight{key: flight}
	fixture.server.transcriptWebCatchupMu.Unlock()
	var release sync.Once
	releaseFlight := func() {
		release.Do(func() {
			fixture.server.transcriptWebCatchupMu.Lock()
			delete(fixture.server.transcriptWebCatchups, key)
			close(flight.done)
			fixture.server.transcriptWebCatchupMu.Unlock()
		})
	}
	t.Cleanup(releaseFlight)

	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "live_request", true: "canceled_request"}[canceled], func(t *testing.T) {
			request := newLoopbackTestRequest(http.MethodGet, "/api/frames/"+transcriptWebServingFrameID, nil)
			request.RemoteAddr = "127.0.0.1:12345"
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			response := httptest.NewRecorder()
			started := time.Now()
			fixture.server.Handler().ServeHTTP(response, request.WithContext(ctx))
			elapsed := time.Since(started)
			if response.Code != http.StatusOK {
				t.Fatalf("terminal snapshot waited for catch-up instead of reporting pending presentation: status=%d elapsed=%s body=%s",
					response.Code, elapsed, response.Body.String())
			}
			requirePendingTerminalPresentation(t, response)
			if elapsed >= time.Second {
				t.Fatalf("terminal snapshot joined history catch-up: elapsed=%s", elapsed)
			}
			t.Logf("pending frame GET status=%d elapsed=%s", response.Code, elapsed)
			select {
			case <-flight.done:
				t.Fatal("status request or cancellation settled the shared history owner")
			default:
			}
		})
	}
	if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID); after != before {
		t.Fatalf("frame GET rebuilt history: before=%#v after=%#v", before, after)
	}
	foreign := p3JSONRequest(t, fixture.server, http.MethodGet,
		"/api/frames/"+transcriptWebServingFrameID, nil, "foreign-owner")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign terminal snapshot status=%d body=%s", foreign.Code, foreign.Body.String())
	}

	releaseFlight()
	history := p3JSONRequest(t, fixture.server, http.MethodGet,
		"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
	if history.Code != http.StatusOK {
		t.Fatalf("normal history owner could not publish final presentation: status=%d body=%s", history.Code, history.Body.String())
	}
	ready := p3JSONRequest(t, fixture.server, http.MethodGet,
		"/api/frames/"+transcriptWebServingFrameID, nil, "")
	payload := p3DecodeObject(t, ready)
	if ready.Code != http.StatusOK || payload["status"] != "completed" ||
		payload["runtime_terminal_presentation_ready"] != true || payload["runtime_active"] != false {
		t.Fatalf("history publication did not release finalizing snapshot: status=%d payload=%#v", ready.Code, payload)
	}
}

func TestCompatibilityTerminalFrameSnapshotDoesNotCreateMissingPresentation(t *testing.T) {
	fixture := newTerminalPresentationFixture(t)
	if _, err := fixture.db.Exec(`DELETE FROM transcript_web_projection_state
		WHERE stream_uid=? AND branch_id=?`, fixture.stream.UID, fixture.fence.BranchID); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, fixture.server, http.MethodGet,
		"/api/frames/"+transcriptWebServingFrameID, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("missing presentation status=%d body=%s", response.Code, response.Body.String())
	}
	requirePendingTerminalPresentation(t, response)
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if fence.StateFound {
		t.Fatalf("frame GET became a competing projector: %#v", fence)
	}
}

func TestTranscriptWebTerminalPresentationReadPreservesOwnerAndCancellation(t *testing.T) {
	fixture := newTerminalPresentationFixture(t)
	foreign := fixture.stream
	foreign.OwnerID = "foreign-owner"
	if ready, err := fixture.server.transcriptWebTerminalPresentationReady(t.Context(), foreign); ready || !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		t.Fatalf("foreign terminal readiness=%t err=%v", ready, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ready, err := fixture.server.transcriptWebTerminalPresentationReady(ctx, fixture.stream); ready || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled terminal readiness=%t err=%v", ready, err)
	}
	if ready, err := fixture.server.transcriptWebTerminalPresentationReady(t.Context(), fixture.stream); ready || err != nil {
		t.Fatalf("stale terminal readiness=%t err=%v", ready, err)
	}
}

func TestTranscriptWebTerminalPresentationReadUsesCurrentBranch(t *testing.T) {
	fixture := newTerminalPresentationFixture(t)
	if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ready, err := fixture.server.transcriptWebTerminalPresentationReady(t.Context(), fixture.stream); !ready || err != nil {
		t.Fatalf("initial terminal readiness=%t err=%v", ready, err)
	}
	fork, err := fixture.repository.ForkFrameUserMessageAtIndex(t.Context(), transcriptstore.ForkFrameUserMessageAtIndexInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
		SourceBranchID: fixture.fence.BranchID, ExpectedActiveBranchID: fixture.fence.BranchID,
		ExpectedGeneration: fixture.fence.BranchGeneration, ClientMutationID: "terminal-presentation-fork",
		SourceMessageIndex: 0, ReplacementText: "new branch input", Destinations: []string{"ws"},
	})
	if err != nil || !fork.Created || fork.BranchID == fixture.fence.BranchID {
		t.Fatalf("fork=%#v err=%v", fork, err)
	}
	if ready, err := fixture.server.transcriptWebTerminalPresentationReady(t.Context(), fixture.stream); ready || err != nil {
		t.Fatalf("previous ready branch escaped current-branch fence: ready=%t err=%v", ready, err)
	}
}

func newTerminalPresentationFixture(t *testing.T) transcriptWebReadModelServingFixture {
	t.Helper()
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
		RunnerID: "terminal-snapshot-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	if _, _, created, err := fixture.repository.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: claim.Claim, ClientMessageID: "terminal-snapshot-finished", Status: "completed",
		PayloadJSON: []byte(`{"summary":"task complete"}`), Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("finish created=%t err=%v", created, err)
	}
	return fixture
}

func requirePendingTerminalPresentation(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	payload := p3DecodeObject(t, response)
	if payload["status"] != "finalizing" || payload["runtime_terminal_presentation_ready"] != false ||
		payload["runtime_active"] != false || payload["runtime_finished_at"] == nil {
		t.Fatalf("pending terminal snapshot=%#v", payload)
	}
}
