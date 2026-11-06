package server

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebBackgroundAndRequestShareFullRebuild(t *testing.T) {
	fixture := newTranscriptWebSharedFullRebuildFixture(t)
	releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
	defer releaseWriter()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	background := make(chan error, 1)
	go func() { background <- fixture.server.runTranscriptWebReadModelBatch(ctx, 1) }()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, false)
	request := make(chan error, 1)
	go func() {
		request <- fixture.server.catchUpTranscriptWebReadModel(ctx,
			fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
	}()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, true)
	releaseWriter()
	awaitTranscriptWebSharedRebuildResult(t, background)
	awaitTranscriptWebSharedRebuildResult(t, request)
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if !transcriptWebFenceReady(fence) || fence.StateMessageCount != 2 {
		t.Fatalf("shared rebuild lost canonical tool history: %#v", fence)
	}
}

func TestTranscriptWebBackgroundSkipsRequestOwnedFullRebuild(t *testing.T) {
	fixture := newTranscriptWebSharedFullRebuildFixture(t)
	releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
	defer releaseWriter()
	request := make(chan error, 1)
	go func() {
		request <- fixture.server.catchUpTranscriptWebReadModel(t.Context(),
			fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
	}()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, false)
	background := make(chan error, 1)
	go func() { background <- fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1) }()
	// The global worker must return to its other delivery duties before the
	// request-owned projection's write is released.
	awaitTranscriptWebSharedRebuildResult(t, background)
	select {
	case err := <-request:
		t.Fatalf("background released the request projection owner: %v", err)
	default:
	}
	releaseWriter()
	awaitTranscriptWebSharedRebuildResult(t, request)
	fixture.server.transcriptWebCatchupMu.Lock()
	remaining := len(fixture.server.transcriptWebCatchups)
	fixture.server.transcriptWebCatchupMu.Unlock()
	if remaining != 0 {
		t.Fatal("completed shared projection retained an owner")
	}
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if !transcriptWebFenceReady(fence) || fence.StateMessageCount != 2 {
		t.Fatalf("request-owned full rebuild did not retain canonical messages: %#v", fence)
	}
}

func TestTranscriptWebCanceledFollowerKeepsBackgroundFullRebuild(t *testing.T) {
	fixture := newTranscriptWebSharedFullRebuildFixture(t)
	releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
	defer releaseWriter()
	background := make(chan error, 1)
	go func() { background <- fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1) }()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	canceledRequest := make(chan error, 1)
	go func() {
		canceledRequest <- fixture.server.catchUpTranscriptWebReadModel(ctx,
			fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
	}()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, true)
	cancel()
	select {
	case err := <-canceledRequest:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follower cancellation changed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled follower remained in shared repair")
	}
	select {
	case err := <-background:
		t.Fatalf("request cancellation ended background-owned projection: %v", err)
	default:
	}
	nextRequest := make(chan error, 1)
	go func() {
		nextRequest <- fixture.server.catchUpTranscriptWebReadModel(t.Context(),
			fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
	}()
	awaitTranscriptWebSharedRebuildStack(t, releaseWriter, true)
	releaseWriter()
	awaitTranscriptWebSharedRebuildResult(t, background)
	awaitTranscriptWebSharedRebuildResult(t, nextRequest)
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if !transcriptWebFenceReady(fence) || fence.StateMessageCount != 2 {
		t.Fatalf("replacement reader lost the completed background projection: %#v", fence)
	}
}

func newTranscriptWebSharedFullRebuildFixture(t *testing.T) transcriptWebReadModelServingFixture {
	t.Helper()
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	readModel, err := fixture.store.TranscriptWebReadModel(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixture.readModel, fixture.server.transcriptWebReadModel = readModel, readModel
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "shared-history-reader",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim synthetic runner: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerToolCheckpoint(t, fixture.repository, claim.Claim, "shared-history-tool", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "shared-history-call",
		"toolName": "read_file", "toolInput": map[string]any{"path": "synthetic.txt"},
		"toolResult": map[string]any{"content": "synthetic tool result"},
	})
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
	if err != nil || result.Disposition != transcriptWebIncrementalFallbackTool {
		t.Fatalf("actual tool checkpoint did not require full rebuild: result=%#v err=%v", result, err)
	}
	return fixture
}

// The writer is held only after real source preparation. Both owners can read
// the WAL history; the stack barrier pins the actual final atomic Apply rather
// than manufacturing a slow history or relying on a sleep.
func awaitTranscriptWebSharedRebuildStack(t *testing.T, release func(), requestStarted bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	stack := make([]byte, 512<<10)
	for {
		builders, followers := 0, 0
		for _, goroutine := range strings.Split(string(stack[:runtime.Stack(stack, true)]), "\n\n") {
			if strings.Contains(goroutine, "(*Server).rebuildTranscriptWebReadModel(") &&
				strings.Contains(goroutine, "(*WebReadModelRepository).ApplyTranscriptWebProjection(") {
				builders++
			} else if strings.Contains(goroutine, "(*Server).catchUpTranscriptWebReadModel(") &&
				strings.Contains(goroutine, "[select]") {
				followers++
			}
		}
		if builders > 1 {
			t.Errorf("same-stream request and background independently rebuilt canonical history: builders=%d", builders)
			return
		}
		if builders == 1 && (!requestStarted || followers == 1) {
			return
		}
		select {
		case <-deadline.C:
			release()
			t.Fatalf("shared rebuild did not reach bounded Apply/follower barrier: builders=%d followers=%d", builders, followers)
		default:
			runtime.Gosched()
		}
	}
}

func awaitTranscriptWebSharedRebuildResult(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, transcriptstore.ErrBranchStateStale) &&
			!errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
			t.Fatalf("rebuild did not settle through its canonical fence: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rebuild did not finish after writer release")
	}
}
