package server

import (
	"context"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func newDelegatedDrainChild(t *testing.T) (*agentSaveArtifactsFixture, workspace.KernelSupervisedChild, *workspace.DelegatedStopTarget) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	children, err := f.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: f.stream.FrameID, OwnerUserID: f.stream.OwnerID, ToolUseID: "stop-drain-handoff",
		Requests: []workspace.KernelDelegateRequest{{Task: "Real ownership handoff"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("create real child=%#v err=%v", children, err)
	}
	initial, err := f.store.KernelChildInitialTarget(t.Context(), children[0])
	if err != nil || initial == nil {
		t.Fatalf("real child origin=%#v err=%v", initial, err)
	}
	return f, children[0], initial
}

func admitDelegatedDrainStop(t *testing.T, f *agentSaveArtifactsFixture, child workspace.KernelSupervisedChild, initial *workspace.DelegatedStopTarget) workspace.DelegatedStopReceipt {
	t.Helper()
	scope := workspace.DelegatedStopScope{ParentFrameID: f.identity.access.Frame.ID,
		ParentFrameIncarnationID: f.identity.access.Frame.IncarnationID, RootFrameID: f.identity.access.Frame.RootFrameID,
		RootFrameIncarnationID: f.identity.access.RootFrameIncarnationID}
	receipt, err := f.store.AdmitKernelChildStop(t.Context(), child.OwnerUserID, scope, child.FrameID, "Stop owned execution", "drain-handoff-call", initial)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.CancelRunner(t.Context(), transcriptstore.CancelRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		ExpectedAttempt: f.claim.Attempt, ClientMessageID: "release-fixture-parent", ReasonCode: "owned_fixture_complete", Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := f.repo.CountActiveRunnerAttempts(t.Context()); err != nil || count != 0 {
		t.Fatalf("unrelated durable ownership would invalidate the drain oracle: count=%d err=%v", count, err)
	}
	return receipt
}

func blockDelegatedProofStorage(t *testing.T, f *agentSaveArtifactsFixture) func() {
	t.Helper()
	locked, release, exited := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	go func() {
		exited <- f.repo.RunImmediate(t.Context(), func(_ *transcriptstore.ImmediateTransaction) error {
			close(locked)
			<-release
			return nil
		})
	}()
	t.Cleanup(func() {
		unlock()
		if err := <-exited; err != nil {
			t.Error(err)
		}
	})
	awaitDelegatedOwnedExit(t, locked)
	return unlock
}

func waitDelegatedProofAndIdle(t *testing.T, f *agentSaveArtifactsFixture, child workspace.KernelSupervisedChild, original workspace.DelegatedStopReceipt) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		receipt, found, err := f.server.readDelegatedStopReceipt(ctx, child.OwnerUserID, child.ParentFrameID, original.Request.RequestID)
		if err != nil || !found {
			t.Fatalf("read original proof after storage release: found=%t err=%v", found, err)
		}
		_, operations, _ := f.server.runtimeActivity()
		if receipt.State == "settled" && operations == 0 {
			if receipt.Targets[0].Target != original.Targets[0].Target || !f.server.TryDrainIdle() {
				t.Fatal("completed proof changed identity or leaked runtime ownership")
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("real cleanup/writer did not finish: receipt=%#v operations=%d", receipt, operations)
		case <-tick.C:
		}
	}
}

func waitDelegatedFailedProofWithoutOwner(t *testing.T, f *agentSaveArtifactsFixture, child workspace.KernelSupervisedChild, original workspace.DelegatedStopReceipt) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		proofs := f.server.pendingDelegatedStopProofs(child.OwnerUserID, original)
		_, operations, _ := f.server.runtimeActivity()
		if len(proofs) == 1 {
			proofs[0].mu.Lock()
			writing, err := proofs[0].writing, proofs[0].err
			proofs[0].mu.Unlock()
			if !writing && err != nil && operations == 0 {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("real failed write did not finish/release ownership: proofs=%d operations=%d", len(proofs), operations)
		case <-tick.C:
		}
	}
}
