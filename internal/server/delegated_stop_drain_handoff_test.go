package server

import (
	"sync"
	"testing"
)

func TestDelegatedStopCleanupHandsOwnershipToProofWriter(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		f, child, initial := newDelegatedDrainChild(t)
		ownerCtx, _, finishOwner := delegatedActiveRun(t, f, child, "drain-handoff-owner")
		active := ownerCtx.Value(activeSessionRunContextKey{}).(*activeSessionRun)
		receipt := admitDelegatedDrainStop(t, f, child, initial)
		// The original 30-second window is covered by the unchanged late-owner
		// regression. This test isolates its remaining registration callback.
		binding := &delegatedStopCompletion{server: f.server, owner: child.OwnerUserID, receipt: freezeDelegatedStopReceipt(receipt),
			index: 0, registrations: []delegatedStopRegistration{{active.done, &active.stopCompletion}}}
		binding.subscribe()
		physicalGate, exited := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(physicalGate) }) }
		go func() { <-ownerCtx.Done(); <-physicalGate; finishOwner(); close(exited) }()
		held := false
		t.Cleanup(func() {
			if held {
				active.settlement.Unlock()
				held = false
			}
			active.cancel(ErrGenerationStopped)
			release()
			awaitDelegatedOwnedExit(t, exited)
		})
		// Hold the original post-done publication boundary without a new hook.
		active.settlement.Lock()
		held = true
		active.cancel(ErrGenerationStopped)
		release()
		awaitDelegatedOwnedExit(t, active.done)
		if sessions, operations, _ := f.server.runtimeActivity(); sessions != 0 || operations != 1 {
			t.Fatalf("map removal exposed an unowned cleanup: sessions=%d operations=%d", sessions, operations)
		}
		if f.server.TryDrainIdle() {
			t.Fatal("idle drain crossed the actual owner's cleanup boundary")
		}
		if _, err := f.db.Exec(`CREATE TRIGGER reject_planned_stop_observation BEFORE UPDATE ON frame_events WHEN OLD.event_type='delegated_stop_receipt' BEGIN SELECT RAISE(ABORT,'controlled planned-reload observation rejection'); END`); err != nil {
			t.Fatal(err)
		}
		unlockStorage := blockDelegatedProofStorage(t, f)
		active.settlement.Unlock()
		held = false
		awaitDelegatedOwnedExit(t, exited)
		if _, operations, _ := f.server.runtimeActivity(); operations < 1 || f.server.TryDrainIdle() {
			t.Fatal("cleanup released ownership before the finite writer was admitted")
		}
		unlockStorage()
		waitDelegatedFailedProofWithoutOwner(t, f, child, receipt)
		if f.server.TryDrainIdle() {
			t.Fatal("planned reload discarded an unacknowledged completed proof")
		}
		if _, operations, draining := f.server.runtimeActivity(); operations != 0 || draining {
			t.Fatal("failed observation flush fabricated an active owner or retained drain intent")
		}
		if _, err := f.db.Exec(`DROP TRIGGER reject_planned_stop_observation`); err != nil {
			t.Fatal(err)
		}
		if !f.server.TryDrainIdle() {
			t.Fatal("explicit planned reload did not flush the original retained proof after storage recovered")
		}
		stored, found, err := f.store.ReadDelegatedStop(t.Context(), child.OwnerUserID, child.ParentFrameID, receipt.Request.RequestID)
		if err != nil || !found || stored.State != "settled" || stored.Targets[0].Target != receipt.Targets[0].Target {
			t.Fatalf("planned reload did not acknowledge original real completion: receipt=%#v found=%t err=%v", stored, found, err)
		}
	})

	t.Run("supervisor", func(t *testing.T) {
		f, child, initial := newDelegatedDrainChild(t)
		f.server.startKernelChildRun(child, make(chan struct{}))
		key := kernelChildRunKey{server: f.server, frameID: child.FrameID}
		registered, found := kernelChildRuns.Load(key)
		if !found {
			t.Fatal("actual gated supervisor did not register")
		}
		run := registered.(*kernelChildRun)
		t.Cleanup(func() { run.cancel(); awaitDelegatedOwnedExit(t, run.done) })
		receipt := admitDelegatedDrainStop(t, f, child, initial)
		binding := &delegatedStopCompletion{server: f.server, owner: child.OwnerUserID, receipt: freezeDelegatedStopReceipt(receipt),
			index: 0, registrations: []delegatedStopRegistration{{run.done, &run.stopCompletion}}}
		binding.subscribe()
		if f.server.TryDrainIdle() {
			t.Fatal("genuine queued supervisor was invisible to idle admission")
		}
		unlockStorage := blockDelegatedProofStorage(t, f)
		if !run.markCanonicalStop(receipt.Targets[0].Target) {
			t.Fatal("original supervisor input changed")
		}
		run.stop()
		awaitDelegatedOwnedExit(t, run.done)
		if _, operations, _ := f.server.runtimeActivity(); operations < 1 || f.server.TryDrainIdle() {
			t.Fatal("supervisor map removal exposed its real cleanup as idle")
		}
		unlockStorage()
		waitDelegatedProofAndIdle(t, f, child, receipt)
	})
}
