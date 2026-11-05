package server

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebFullProjectionDoesNotWaitForUnrelatedWriter(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "full-projection-reader",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim synthetic runner: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerToolCheckpoint(t, fixture.repository, claim.Claim, "full-projection-tool", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "full-projection-call",
		"toolName": "read_file", "toolInput": map[string]any{"path": "synthetic.txt"},
		"toolResult": map[string]any{"content": "synthetic tool result"},
	})
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	incremental, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
	if err != nil || incremental.Disposition != transcriptWebIncrementalFallbackTool {
		t.Fatalf("ordinary tool did not require the real full projection: disposition=%s err=%v", incremental.Disposition, err)
	}
	type result struct {
		messages []map[string]any
		snapshot transcriptstore.ProjectionSnapshot
		metadata transcriptWebProjectionSourceMetadata
		err      error
	}
	project := func(ctx context.Context) result {
		messages, snapshot, err := fixture.server.projectTranscriptWebHistory(ctx, fixture.stream,
			fixture.stream.OwnerID, fixture.stream.SessionID, fixture.fence.BranchID)
		var metadata transcriptWebProjectionSourceMetadata
		if err == nil {
			metadata, err = fixture.server.projectTranscriptWebSourceMetadata(ctx, fixture.stream,
				fixture.stream.OwnerID, fixture.stream.SessionID, snapshot)
		}
		return result{messages, snapshot, metadata, err}
	}
	expected := project(t.Context())
	if expected.err != nil || len(expected.messages) != 2 {
		t.Fatalf("uncontended full projection: messages=%d err=%v", len(expected.messages), expected.err)
	}
	before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
	releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
	defer releaseWriter()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan result, 1)
	go func() { done <- project(ctx) }()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	stack := make([]byte, 256<<10)
	var actual result
	for {
		select {
		case actual = <-done:
			goto settled
		case <-deadline.C:
			t.Error("read-only full projection did not settle while unrelated writer was held")
			releaseWriter()
			goto awaitReleased
		default:
		}
		for _, goroutine := range strings.Split(string(stack[:runtime.Stack(stack, true)]), "\n\n") {
			if strings.Contains(goroutine, "(*Repository).HasActiveTypedHistoryBootstrap") &&
				strings.Contains(goroutine, "database/sql.(*DB).conn") && strings.Contains(goroutine, "[select]") {
				t.Errorf("read-only full projection waits for the writer connection:\n%s", goroutine)
				releaseWriter()
				goto awaitReleased
			}
		}
		runtime.Gosched()
	}
awaitReleased:
	select {
	case actual = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("full projection did not settle after controlled writer cleanup")
	}
settled:
	releaseWriter()
	if actual.err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("writer-independent full projection changed its canonical result: error=%v", actual.err)
	}
	if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID); after != before {
		t.Fatal("read-only full projection modified its projection state")
	}
}
