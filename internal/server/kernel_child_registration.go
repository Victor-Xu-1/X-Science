package server

import (
	"context"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) startKernelChildRun(child workspace.KernelSupervisedChild, gates ...chan struct{}) {
	if s.isDraining() {
		return
	}
	preflight, finishPreflight := context.WithTimeout(context.Background(), 5*time.Second)
	target, status, targetErr := s.workspaceStore.KernelChildDispatchTarget(preflight, child.OwnerUserID, child.ParentFrameID, child.FrameID)
	finishPreflight()
	if targetErr != nil || status == "terminal" {
		return
	}
	key := kernelChildRunKey{server: s, frameID: child.FrameID}
	ctx, cancelCause := context.WithCancelCause(context.Background())
	cancel := func() { cancelCause(context.Canceled) }
	run := &kernelChildRun{target: &target, stop: func() { cancelCause(ErrGenerationStopped) }, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	ctx = context.WithValue(ctx, kernelChildOwnerContextKey{}, run)
	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsMu.Unlock()
		cancel()
		return
	}
	existing, loaded := kernelChildRuns.LoadOrStore(key, run)
	s.sessionRunsMu.Unlock()
	if loaded {
		cancel()
		existing.(*kernelChildRun).signal()
		return
	}
	_ = s.workspaceStore.MarkKernelChildDispatched(context.Background(), child.FrameID, child.OwnerUserID)
	go func() {
		defer s.finishKernelChildRegistration(ctx, child, run)
		if len(gates) > 0 && gates[0] != nil {
			select {
			case gates[0] <- struct{}{}:
				defer func() { <-gates[0] }()
			case <-ctx.Done():
				if run.stopSnapshot() == nil {
					s.settleKernelChildRun(child, "cancelled", nil, "queued child run was cancelled", run.completionFences()...)
				}
				return
			}
		}
		s.runKernelChildSupervisor(ctx, child, run)
	}()
}
