package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopCommittedContinuationAndQueuedOwnerSurviveIdleEvaluation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "accepted-during-idle-evaluation",
		Requests: []workspace.KernelDelegateRequest{{Task: "Accepted Stop continuation"}, {Task: "Accepted queued sibling"}},
	})
	if err != nil || len(children) != 2 {
		t.Fatalf("actual accepted child admission=%#v err=%v", children, err)
	}
	_, err = f.repo.CancelRunner(t.Context(), transcriptstore.CancelRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		ExpectedAttempt: f.claim.Attempt, ClientMessageID: "idle-evaluation-parent-done", ReasonCode: "owned_fixture_complete", Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := f.repo.CountActiveRunnerAttempts(t.Context()); err != nil || count != 0 {
		t.Fatalf("unrelated durable owner invalidates idle oracle: count=%d err=%v", count, err)
	}
	entered, releaseEvaluation, evaluationDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseEvaluation) }) }
	f.server.tryDrainIdleDurableWait = func() { close(entered); <-releaseEvaluation }
	drained := make(chan bool, 1)
	go func() { drained <- f.server.TryDrainIdle(); close(evaluationDone) }()
	t.Cleanup(func() { release(); awaitDelegatedOwnedExit(t, evaluationDone) })
	awaitDelegatedOwnedExit(t, entered)
	if finish, err := f.server.beginRuntimeOperation(); !errors.Is(err, ErrRuntimeDraining) {
		if finish != nil {
			finish()
		}
		t.Fatalf("new business admission crossed the original evaluation fence: %v", err)
	}
	var supervisors []*kernelChildRun
	t.Cleanup(func() {
		for _, run := range supervisors {
			run.cancel()
		}
		for _, run := range supervisors {
			awaitDelegatedOwnedExit(t, run.done)
		}
	})
	// Both delegates were durably admitted before evaluation. Register their
	// genuine gated owners now; returning a dispatched child without an owner
	// would lose the accepted work instead of deferring the final idle check.
	for _, child := range children {
		f.server.startKernelChildRun(child, make(chan struct{}))
		value, found := kernelChildRuns.Load(kernelChildRunKey{server: f.server, frameID: child.FrameID})
		if !found {
			t.Fatal("accepted child was dropped during idle evaluation")
		}
		supervisors = append(supervisors, value.(*kernelChildRun))
		stored, found, err := f.store.GetKernelSupervisedChild(t.Context(), child.ParentFrameID, child.FrameID, child.OwnerUserID)
		if err != nil || !found || !stored.Dispatched {
			t.Fatalf("actual queued owner and dispatch readback disagree: child=%#v err=%v", stored, err)
		}
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(t.Context(), child)
	if err != nil || initial == nil {
		t.Fatalf("actual original input=%#v err=%v", initial, err)
	}
	ownerCtx, _, finishOwner := delegatedActiveRun(t, f, child, "accepted-stop-runner", supervisors[0])
	ownerExited := make(chan struct{})
	go func() { <-ownerCtx.Done(); finishOwner(); close(ownerExited) }()
	t.Cleanup(func() {
		f.server.stopActiveSessionRun(child.FrameID, "accepted owner fixture cleanup")
		awaitDelegatedOwnedExit(t, ownerExited)
	})
	scope := workspace.DelegatedStopScope{ParentFrameID: f.identity.access.Frame.ID,
		ParentFrameIncarnationID: f.identity.access.Frame.IncarnationID, RootFrameID: f.identity.access.Frame.RootFrameID,
		RootFrameIncarnationID: f.identity.access.RootFrameIncarnationID}
	receipt, err := f.store.AdmitKernelChildStop(t.Context(), child.OwnerUserID, scope, child.FrameID,
		"Accepted cancellation", "accepted-evaluation-stop", initial)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := f.server.signalDelegatedStop(t.Context(), child.OwnerUserID, &receipt)
	if err != nil || !errors.Is(context.Cause(ownerCtx), ErrGenerationStopped) {
		t.Fatalf("committed Stop was revoked by idle evaluation: err=%v cause=%v", err, context.Cause(ownerCtx))
	}
	awaitDelegatedOwnedExit(t, ownerExited)
	awaitDelegatedOwnedExit(t, supervisors[0].done)
	awaitDelegatedOwnedExit(t, observed)
	stored, found, err := f.server.readDelegatedStopReceipt(t.Context(), child.OwnerUserID, child.ParentFrameID, receipt.Request.RequestID)
	if err != nil || !found || stored.State != "settled" || stored.Targets[0].Target != receipt.Targets[0].Target {
		t.Fatalf("accepted completed-proof continuation failed during evaluation: receipt=%#v found=%t err=%v", stored, found, err)
	}
	select {
	case <-supervisors[1].done:
		t.Fatal("stopping one accepted child cancelled the queued sibling")
	default:
	}
	release()
	awaitDelegatedOwnedExit(t, evaluationDone)
	if <-drained {
		t.Fatal("idle evaluation ignored the genuinely queued accepted sibling")
	}
	f.server.tryDrainIdleDurableWait = nil
	if f.server.TryDrainIdle() {
		t.Fatal("remaining real queued owner was reported idle")
	}
	result := f.server.stopKernelChildTarget(t.Context(), f.identity.access, children[1].FrameID,
		"Finish owned queued fixture", "accepted-sibling-stop", nil)
	if result["status"] != "stopped" {
		t.Fatalf("actual queued owner failed its normal Stop cleanup: %#v", result)
	}
	awaitDelegatedOwnedExit(t, supervisors[1].done)
	waitDelegatedProofAndIdle(t, f, child, receipt)
	if finish, err := f.server.beginAcceptedStopContinuation(); !errors.Is(err, ErrRuntimeDraining) {
		if finish != nil {
			finish()
		}
		t.Fatalf("accepted continuation crossed actual draining: %v", err)
	}
}
