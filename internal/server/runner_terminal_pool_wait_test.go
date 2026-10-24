package server

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerFinalizationReturnsBoundedErrorWhileStorePoolBusy(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	if fixture.server.kernelManager != nil || len(fixture.server.agentRuntimeHooksForEvent("Stop", "")) != 0 {
		t.Fatal("SETUP_FAILURE: fixture must not run kernels or Stop hooks")
	}
	fixture.server.memoryExtraction = nil
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	active := &activeSessionRun{}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	held, release := make(chan struct{}), make(chan struct{})
	holderDone := make(chan error, 1)
	var releaseOnce sync.Once
	releaseHolder := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHolder()
	go func() {
		_, err := fixture.repo.RunImmediateWithOutcome(context.Background(), func(*transcriptstore.ImmediateTransaction) error { close(held); <-release; return nil })
		holderDone <- err
	}()
	select {
	case <-held:
	case err := <-holderDone:
		t.Fatalf("SETUP_FAILURE: holder returned: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("SETUP_FAILURE: real Store writer pool not held")
	}
	settleDone := make(chan error, 1)
	identity := make(chan string, 1)
	go func() {
		buffer := make([]byte, 96)
		header := strings.Fields(string(buffer[:runtime.Stack(buffer, false)]))
		identity <- "goroutine " + header[1] + " ["
		settleDone <- fixture.server.settleSessionRunnerChatOutcome(parent, SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result, active,
			sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, "Complete unacknowledged synthetic answer.", nil, "")
	}()
	prefix := <-identity
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	setup := time.NewTimer(3 * time.Second)
	defer setup.Stop()
findAtomicPoolWait:
	for {
		buffer := make([]byte, 1024*1024)
		for _, stack := range strings.Split(string(buffer[:runtime.Stack(buffer, true)]), "\n\n") {
			if strings.HasPrefix(stack, prefix) && strings.Contains(stack, "database/sql.(*DB).conn(") &&
				strings.Contains(stack, "RunImmediateWithOutcome(") && strings.Contains(stack, "finalizeRunnerTranscriptWithRunner(") && strings.Contains(stack, "settleSessionRunnerChatOutcome(") {
				t.Logf("exact atomic-finalization pool wait:\n%s", stack)
				break findAtomicPoolWait
			}
		}
		select {
		case err := <-settleDone:
			releaseHolder()
			<-holderDone
			t.Fatalf("SETUP_FAILURE: finalization returned before actual pool boundary: %v", err)
		case <-setup.C:
			releaseHolder()
			<-holderDone
			<-settleDone
			t.Fatal("SETUP_FAILURE: exact atomic pool wait not observed")
		case <-tick.C:
		}
	}
	cancelParent()
	var settleErr error
	returned := false
	select {
	case settleErr = <-settleDone:
		returned = true
	case <-time.After(sessionRunnerContentDeltaPersistenceTimeout + 2*time.Second):
		t.Error("atomic finalization exceeded its finite persistence observation window")
	}
	releaseHolder()
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if !returned {
		settleErr = <-settleDone
	}
	if !errors.Is(settleErr, context.DeadlineExceeded) || active.settled || result.FinishEventID != 0 {
		t.Fatalf("unacknowledged answer was accepted or lost its real storage error: result=%#v settled=%t err=%v", result, active.settled, settleErr)
	}
	for _, query := range []string{`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='assistant_message'`, `SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_finished'`, `SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`} {
		var count int
		if err := fixture.db.QueryRow(query, fixture.stream.UID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("uncommitted finalization left partial rows: count=%d err=%v", count, err)
		}
	}
	stream, err := fixture.repo.GetStream(t.Context(), fixture.stream.UID, fixture.stream.OwnerID)
	if err != nil || stream.InputRevision <= 0 || stream.ConsumedInputRevision != 0 {
		t.Fatalf("original input/progress not retained or pool unavailable after release: stream=%#v err=%v", stream, err)
	}
	t.Log("holder and finalizer joined; no answer/terminal accepted; original input and usable Store retained")
}
