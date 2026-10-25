package server

import (
	"context"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func kernelChildSameInput(a, b *workspace.DelegatedStopTarget) bool {
	return a != nil && b != nil && a.FrameID == b.FrameID && a.FrameIncarnationID == b.FrameIncarnationID &&
		a.StreamUID == b.StreamUID && a.StreamEpoch == b.StreamEpoch && a.InputMessageID == b.InputMessageID && a.InputRevision == b.InputRevision
}

func (r *kernelChildRun) markCanonicalStop(target workspace.DelegatedStopTarget) bool {
	r.targetMu.Lock()
	defer r.targetMu.Unlock()
	return r.markCanonicalStopLocked(target)
}

func (r *kernelChildRun) markCanonicalStopLocked(target workspace.DelegatedStopTarget) bool {
	if !kernelChildSameInput(r.target, &target) {
		return false
	}
	r.canonicalStop = &target
	return true
}

func (r *kernelChildRun) stopSnapshot() *workspace.DelegatedStopTarget {
	r.targetMu.Lock()
	defer r.targetMu.Unlock()
	if r.canonicalStop == nil {
		return nil
	}
	target := *r.canonicalStop
	return &target
}

func (r *kernelChildRun) completionTargets() []workspace.DelegatedStopTarget {
	r.targetMu.Lock()
	defer r.targetMu.Unlock()
	if r.target == nil {
		return nil
	}
	return []workspace.DelegatedStopTarget{*r.target}
}

func (r *kernelChildRun) completionFences() []workspace.KernelChildCompletionFence {
	r.targetMu.Lock()
	defer r.targetMu.Unlock()
	if r.target == nil {
		return nil
	}
	return []workspace.KernelChildCompletionFence{{Target: *r.target, Claim: r.completionClaim}}
}

func (s *Server) finishKernelChildRegistration(ctx context.Context, child workspace.KernelSupervisedChild, run *kernelChildRun) {
	stopped := run.stopSnapshot()
	wakePending := ctx.Err() == nil || stopped != nil
	s.sessionRunsMu.Lock()
	// Keep actual supervisor cleanup visible while its map entry is removed,
	// including its existing pending-input handoff and completion callbacks.
	s.activeRuntimeOperations++
	removed := kernelChildRuns.CompareAndDelete(kernelChildRunKey{server: s, frameID: child.FrameID}, run)
	s.sessionRunsMu.Unlock()
	defer s.endRuntimeOperation()
	defer run.stopCompletion.finish()
	run.cancel()
	close(run.done)
	if !removed || !wakePending || s.isDraining() {
		return
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	pending, err := s.workspaceStore.KernelChildHasPendingMessages(readCtx, child.FrameID, child.OwnerUserID)
	if err != nil || !pending {
		return
	}
	target, state, err := s.workspaceStore.KernelChildDispatchTarget(readCtx, child.OwnerUserID, child.ParentFrameID, child.FrameID)
	previous := run.completionTargets()
	if err != nil || state == "terminal" || len(previous) != 1 || target.FrameIncarnationID != previous[0].FrameIncarnationID ||
		target.StreamUID != previous[0].StreamUID || target.StreamEpoch != previous[0].StreamEpoch {
		return
	}
	if stopped != nil && (target.InputRevision <= stopped.InputRevision || target.InputMessageID == stopped.InputMessageID) {
		return
	}
	// Only the existing durable queue can justify this one handoff. Do not
	// reopen the cancelled input, reuse its context or replace another owner.
	s.startKernelChildRun(child)
}
