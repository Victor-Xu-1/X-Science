package server

import (
	"context"
	"sync"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

type delegatedOwnedStopFixture struct {
	f                 *agentSaveArtifactsFixture
	child             workspace.KernelSupervisedChild
	receipt           workspace.DelegatedStopReceipt
	activeCtx         context.Context
	releaseActive     func()
	releaseSupervisor func()
	activeExited      <-chan struct{}
	supervisorExited  <-chan struct{}
	observerDone      <-chan struct{}
}

func newDelegatedOwnedStopFixture(t *testing.T) *delegatedOwnedStopFixture {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "owned-stop-completion",
		Requests: []workspace.KernelDelegateRequest{{Task: "Original owned execution"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("create real child: children=%#v err=%v", children, err)
	}
	child := children[0]
	initial, err := f.store.KernelChildInitialTarget(t.Context(), child)
	if err != nil || initial == nil {
		t.Fatalf("read immutable origin: target=%#v err=%v", initial, err)
	}
	supervisorCtx, cancelSupervisor := context.WithCancelCause(context.Background())
	supervisor := &kernelChildRun{target: initial, done: make(chan struct{}), wake: make(chan struct{}, 1),
		stop: func() { cancelSupervisor(ErrGenerationStopped) }, cancel: func() { cancelSupervisor(context.Canceled) }}
	key := kernelChildRunKey{server: f.server, frameID: child.FrameID}
	if _, loaded := kernelChildRuns.LoadOrStore(key, supervisor); loaded {
		t.Fatal("real owned registration was already present")
	}
	activeCtx, _, finishActive := delegatedActiveRun(t, f, child, "owned-stop-runner", supervisor)
	activeGate, supervisorGate := make(chan struct{}), make(chan struct{})
	activeSignalled, supervisorSignalled := make(chan struct{}), make(chan struct{})
	activeExited, supervisorExited := make(chan struct{}), make(chan struct{})
	var activeOnce, supervisorOnce sync.Once
	owned := &delegatedOwnedStopFixture{f: f, child: child, activeCtx: activeCtx,
		releaseActive:     func() { activeOnce.Do(func() { close(activeGate) }) },
		releaseSupervisor: func() { supervisorOnce.Do(func() { close(supervisorGate) }) },
		activeExited:      activeExited, supervisorExited: supervisorExited}
	// Both registrations own real Go execution. Gates only control their real
	// exit; cancellation and completion use the original production methods.
	go func() {
		<-activeCtx.Done()
		close(activeSignalled)
		<-activeGate
		finishActive()
		close(activeExited)
	}()
	go func() {
		<-supervisorCtx.Done()
		close(supervisorSignalled)
		<-supervisorGate
		f.server.finishKernelChildRegistration(supervisorCtx, child, supervisor)
		close(supervisorExited)
	}()
	t.Cleanup(func() {
		f.server.stopActiveSessionRun(child.FrameID, "owned fixture cleanup")
		supervisor.cancel()
		owned.releaseActive()
		owned.releaseSupervisor()
		for _, exited := range []<-chan struct{}{activeExited, supervisorExited} {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				t.Error("actual fixture owner failed to join")
			}
		}
		if owned.observerDone != nil {
			select {
			case <-owned.observerDone:
			case <-time.After(35 * time.Second):
				t.Error("bounded Stop observer failed to join")
			}
		}
		kernelChildRuns.CompareAndDelete(key, supervisor)
	})
	scope := workspace.DelegatedStopScope{
		ParentFrameID: f.identity.access.Frame.ID, ParentFrameIncarnationID: f.identity.access.Frame.IncarnationID,
		RootFrameID: f.identity.access.Frame.RootFrameID, RootFrameIncarnationID: f.identity.access.RootFrameIncarnationID,
	}
	owned.receipt, err = f.store.AdmitKernelChildStop(t.Context(), child.OwnerUserID, scope, child.FrameID, "Stop captured execution", "owned-stop-call", initial)
	if err != nil || len(owned.receipt.Targets) != 1 || owned.receipt.Targets[0].Outcome != "cancel_requested" {
		t.Fatalf("real Stop admission=%#v err=%v", owned.receipt, err)
	}
	owned.observerDone, err = f.server.signalDelegatedStop(t.Context(), child.OwnerUserID, &owned.receipt)
	if err != nil {
		t.Fatalf("real Stop signal failed: %v", err)
	}
	awaitDelegatedOwnedExit(t, activeSignalled)
	awaitDelegatedOwnedExit(t, supervisorSignalled)
	return owned
}

func awaitDelegatedOwnedExit(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("actual owner did not reach the requested lifecycle boundary")
	}
}

func (o *delegatedOwnedStopFixture) read(t *testing.T) workspace.DelegatedStopReceipt {
	t.Helper()
	receipt, found, err := o.f.store.ReadDelegatedStop(t.Context(), o.child.OwnerUserID, o.child.ParentFrameID, o.receipt.Request.RequestID)
	if err != nil || !found || len(receipt.Targets) != 1 || receipt.Targets[0].Target != o.receipt.Targets[0].Target || receipt.Request.Scope != o.receipt.Request.Scope {
		t.Fatalf("original receipt identity changed: receipt=%#v found=%t err=%v", receipt, found, err)
	}
	return receipt
}
