package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopFailedCompletionWriteSurvivesExactReceiptReplay(t *testing.T) {
	owned := newDelegatedOwnedStopFixture(t)
	f := owned.f
	if _, err := f.db.Exec(`CREATE TRIGGER reject_stop_observation BEFORE UPDATE ON frame_events WHEN OLD.event_type='delegated_stop_receipt' BEGIN SELECT RAISE(ABORT,'controlled observation write rejection'); END`); err != nil {
		t.Fatal(err)
	}
	owned.releaseActive()
	owned.releaseSupervisor()
	awaitDelegatedOwnedExit(t, owned.activeExited)
	awaitDelegatedOwnedExit(t, owned.supervisorExited)
	awaitDelegatedOwnedExit(t, owned.observerDone)
	before := owned.read(t)
	if before.State == "settled" || before.Targets[0].ExecutionObservation == "stopped" {
		t.Fatalf("rejected write was advertised as durable completion: %#v", before)
	}
	proofs := f.server.pendingDelegatedStopProofs(owned.child.OwnerUserID, before)
	if len(proofs) != 1 || proofs[0].result() == nil {
		t.Fatalf("failed real write discarded original physical proof: count=%d", len(proofs))
	}
	path := "/api/frames/" + owned.child.ParentFrameID + "/delegated-stops/" + before.Request.RequestID
	failedRead := p3JSONRequest(t, f.server, http.MethodGet, path, nil, owned.child.OwnerUserID)
	if failedRead.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed readback concealed storage rejection: HTTP%d %s", failedRead.Code, failedRead.Body.String())
	}
	target := before.Targets[0].Target
	failedReplay := f.server.stopKernelChildTarget(t.Context(), f.identity.access, owned.child.FrameID,
		"Stop captured execution", "owned-stop-call", &target)
	if failedReplay["status"] != "stopping" || failedReplay["request_id"] != before.Request.RequestID ||
		failedReplay["error"] == nil || failedReplay["diagnostic"] != "observation_persistence_pending" {
		t.Fatalf("SDK replay concealed persistence failure or lost original request: %#v", failedReplay)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_stop_observation`); err != nil {
		t.Fatal(err)
	}
	// Both original registrations and their bounded observer have already
	// exited. The same SDK request must retry the retained proof, not a task.
	replayed := f.server.stopKernelChildTarget(t.Context(), f.identity.access, owned.child.FrameID,
		"Stop captured execution", "owned-stop-call", &target)
	if replayed["status"] != "stopped" || replayed["request_id"] != before.Request.RequestID {
		t.Fatalf("exact same-request SDK recovery=%#v", replayed)
	}
	readback := p3JSONRequest(t, f.server, http.MethodGet, path, nil, owned.child.OwnerUserID)
	var stored workspace.DelegatedStopReceipt
	if readback.Code != http.StatusOK || json.Unmarshal(readback.Body.Bytes(), &stored) != nil ||
		stored.State != "settled" || stored.Request.RequestID != before.Request.RequestID || stored.Targets[0].Target != target {
		t.Fatalf("canonical HTTP readback lost the original identity: HTTP%d %s", readback.Code, readback.Body.String())
	}
}

func TestDelegatedStopCompletedOwnerSubscriptionDoesNotWaitForStorage(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "completed-stop-subscription",
		Requests: []workspace.KernelDelegateRequest{{Task: "Finish the actual original owner"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("create real child=%#v err=%v", children, err)
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(t.Context(), child)
	if err != nil || initial == nil {
		t.Fatalf("read original child target=%#v err=%v", initial, err)
	}
	ownerCtx, _, finishOwner := delegatedActiveRun(t, f, child, "completed-stop-owner")
	active := ownerCtx.Value(activeSessionRunContextKey{}).(*activeSessionRun)
	ownerExited := make(chan struct{})
	go func() { <-ownerCtx.Done(); finishOwner(); close(ownerExited) }()
	t.Cleanup(func() { active.cancel(ErrGenerationStopped); awaitDelegatedOwnedExit(t, ownerExited) })
	scope := workspace.DelegatedStopScope{ParentFrameID: f.identity.access.Frame.ID,
		ParentFrameIncarnationID: f.identity.access.Frame.IncarnationID, RootFrameID: f.identity.access.Frame.RootFrameID,
		RootFrameIncarnationID: f.identity.access.RootFrameIncarnationID}
	receipt, err := f.store.AdmitKernelChildStop(t.Context(), child.OwnerUserID, scope, child.FrameID, "Finish original owner", "completed-stop-call", initial)
	if err != nil {
		t.Fatal(err)
	}
	// Real canonical admission and the original owner's cancellation/finish
	// precede the deferred observation subscription, as in the attach race.
	active.cancel(ErrGenerationStopped)
	awaitDelegatedOwnedExit(t, ownerExited)
	locked, release, lockExited := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	releaseStorage := func() { releaseOnce.Do(func() { close(release) }) }
	go func() {
		lockExited <- f.repo.RunImmediate(t.Context(), func(_ *transcriptstore.ImmediateTransaction) error {
			close(locked)
			<-release
			return nil
		})
	}()
	t.Cleanup(func() { releaseStorage(); <-lockExited })
	awaitDelegatedOwnedExit(t, locked)
	returned, observerDone := make(chan struct{}), make(chan struct{})
	go func() {
		f.server.startDelegatedStopDrainObservation(child.OwnerUserID, receipt,
			[]delegatedStopDrainObservation{{index: 0, registrations: []delegatedStopRegistration{{active.done, &active.stopCompletion}}}}, observerDone)
		close(returned)
	}()
	t.Cleanup(func() {
		releaseStorage()
		awaitDelegatedOwnedExit(t, returned)
		awaitDelegatedOwnedExit(t, observerDone)
	})
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("completed-owner immediate replay blocked the request defer on storage")
	}
	releaseStorage()
	awaitDelegatedOwnedExit(t, observerDone)
	stored, found, err := f.store.ReadDelegatedStop(context.Background(), child.OwnerUserID, child.ParentFrameID, receipt.Request.RequestID)
	if err != nil || !found || stored.State != "settled" || stored.Targets[0].Target != receipt.Targets[0].Target {
		t.Fatalf("nonblocking subscription lost exact real completion: receipt=%#v found=%t err=%v", stored, found, err)
	}
}
