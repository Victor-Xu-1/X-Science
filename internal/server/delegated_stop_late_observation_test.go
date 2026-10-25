package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopLateOwnerExitAfterObservationSettlesReceipt(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	children, err := f.store.CreateKernelSupervisedChildren(ctx, workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "late-stop-observation",
		Requests: []workspace.KernelDelegateRequest{{Task: "Original delegated owner"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("SETUP_FAILURE: actual child creation=%#v err=%v", children, err)
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(ctx, child)
	if err != nil || initial == nil {
		t.Fatalf("SETUP_FAILURE: committed child origin=%#v err=%v", initial, err)
	}
	childCtx, claim, finishOwner := delegatedActiveRun(t, f, child, "late-stop-owner")
	physicalGate, ownerSignalled, ownerExited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseOwner := func() { releaseOnce.Do(func() { close(physicalGate) }) }
	var observerDone <-chan struct{}
	t.Cleanup(func() {
		releaseOwner()
		finishOwner()
		select {
		case <-ownerExited:
		case <-time.After(3 * time.Second):
			t.Error("original owned goroutine did not join during cleanup")
		}
		if observerDone != nil {
			select {
			case <-observerDone:
			case <-time.After(35 * time.Second):
				t.Error("original bounded observer did not join during cleanup")
			}
		}
	})
	go func() {
		<-childCtx.Done()
		close(ownerSignalled)
		<-physicalGate
		finishOwner()
		close(ownerExited)
	}()
	scope := workspace.DelegatedStopScope{
		ParentFrameID: f.identity.access.Frame.ID, ParentFrameIncarnationID: f.identity.access.Frame.IncarnationID,
		RootFrameID: f.identity.access.Frame.RootFrameID, RootFrameIncarnationID: f.identity.access.RootFrameIncarnationID,
	}
	receipt, err := f.store.AdmitKernelChildStop(ctx, child.OwnerUserID, scope, child.FrameID, "Observe the original owner exiting late", "late-stop-call", initial)
	if err != nil || len(receipt.Targets) != 1 || receipt.Targets[0].Outcome != "cancel_requested" || receipt.Targets[0].Target.RunnerAttempt != claim.Attempt {
		t.Fatalf("SETUP_FAILURE: actual Stop admission=%#v err=%v", receipt, err)
	}
	originalTarget := receipt.Targets[0].Target
	observerDone, err = f.server.signalDelegatedStop(ctx, child.OwnerUserID, &receipt)
	if err != nil {
		t.Fatalf("SETUP_FAILURE: original Stop signal failed: %v", err)
	}
	select {
	case <-ownerSignalled:
	case <-time.After(3 * time.Second):
		t.Fatal("SETUP_FAILURE: Stop did not reach the actual original owner")
	}
	if !errors.Is(context.Cause(childCtx), ErrGenerationStopped) {
		t.Fatalf("SETUP_FAILURE: original owner cause=%v", context.Cause(childCtx))
	}
	// Exercise the real existing observation deadline. The physical owner stays
	// gated until this bounded observer has naturally returned; no sleep or
	// fabricated stopped receipt substitutes for either lifecycle event.
	select {
	case <-observerDone:
	case <-time.After(35 * time.Second):
		t.Fatal("SETUP_FAILURE: original observation window did not finish")
	}
	select {
	case <-ownerExited:
		t.Fatal("SETUP_FAILURE: original physical owner exited before gate release")
	default:
	}
	before, found, err := f.store.ReadDelegatedStop(ctx, child.OwnerUserID, child.ParentFrameID, receipt.Request.RequestID)
	if err != nil || !found || len(before.Targets) != 1 {
		t.Fatalf("SETUP_FAILURE: original receipt before late exit=%#v found=%t err=%v", before, found, err)
	}
	if before.State == "settled" || before.Targets[0].ExecutionObservation == "stopped" {
		t.Errorf("physical stop was confirmed while the original owner was still gated: %#v", before)
	}
	releaseOwner()
	select {
	case <-ownerExited:
	case <-time.After(3 * time.Second):
		t.Fatal("original owner did not finish after its physical gate was released")
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRead()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	lastConfirmed := before
	for {
		stored, found, err := f.store.ReadDelegatedStop(readCtx, child.OwnerUserID, child.ParentFrameID, receipt.Request.RequestID)
		if err == nil && found {
			if len(stored.Targets) != 1 || stored.Request.RequestID != receipt.Request.RequestID || stored.Request.Scope != scope || stored.Targets[0].Target != originalTarget {
				t.Fatalf("late completion was attached to another Stop identity: %#v", stored)
			}
			lastConfirmed = stored
			if stored.State == "settled" && stored.Targets[0].Outcome == "cancelled" && stored.Targets[0].ExecutionObservation == "stopped" {
				return
			}
		}
		if err != nil && readCtx.Err() == nil {
			t.Fatalf("original receipt read failed after actual owner exit: %v", err)
		}
		select {
		case <-readCtx.Done():
			t.Fatalf("late original owner completion did not settle its original Stop receipt: last confirmed receipt=%#v last read err=%v", lastConfirmed, err)
		case <-tick.C:
		}
	}
}
