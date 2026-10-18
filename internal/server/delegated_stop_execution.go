package server

import (
	"context"
	"fmt"
	"log"
	"time"
	workspace "x-science/internal/persistence/workspace"
)

func delegatedStopMatchesRun(target workspace.DelegatedStopTarget, authority *transcriptRunnerAuthority) bool {
	return authority != nil && authority.Stream.FrameID == target.FrameID && authority.Stream.UID == target.StreamUID && authority.Stream.Epoch == target.StreamEpoch &&
		authority.Claim.Attempt == target.RunnerAttempt && authority.Claim.ClaimedInputRevision == target.InputRevision
}

type delegatedStopDrainObservation struct {
	index int
	done  []<-chan struct{}
}

func (s *Server) signalDelegatedStop(ctx context.Context, owner string, receipt *workspace.DelegatedStopReceipt) (<-chan struct{}, error) {
	complete := make(chan struct{})
	if receipt == nil {
		close(complete)
		return complete, nil
	}
	// Admission has committed before this shared SDK/HTTP seam. Transport
	// cancellation cannot revoke that fact, but final exact-identity reads
	// remain bounded and fail closed if storage cannot confirm the target.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var observations []delegatedStopDrainObservation
	defer func() { s.startDelegatedStopDrainObservation(owner, *receipt, observations, complete) }()
	for i := range receipt.Targets {
		outcome := &receipt.Targets[i]
		if outcome.Outcome != "cancel_requested" {
			continue
		}
		target := outcome.Target
		observations = append(observations, delegatedStopDrainObservation{index: i})
		observation := &observations[len(observations)-1]
		matched := false
		var supervisor *kernelChildRun
		if target.RunnerAttempt == 0 {
			if value, found := kernelChildRuns.Load(kernelChildRunKey{server: s, frameID: target.FrameID}); found {
				supervisor = value.(*kernelChildRun)
			}
		}
		s.sessionRunsMu.Lock()
		if run := s.sessionRuns[target.FrameID]; run != nil {
			run.settlement.Lock()
			if !run.settled && delegatedStopMatchesRun(target, run.transcript) {
				allowed, err := s.workspaceStore.DelegatedStopSignalAllowed(ctx, owner, *receipt, target, &run.transcript.Claim)
				if err != nil {
					run.settlement.Unlock()
					s.sessionRunsMu.Unlock()
					return complete, err
				}
				if allowed {
					// Capture the registration which owns this exact runner. A
					// replacement installed by cancellation/cleanup is unrelated.
					supervisor = run.delegatedOwner
					if supervisor != nil && !supervisor.markCanonicalStop(target) {
						supervisor = nil
					}
					run.cancel(fmt.Errorf("%w: exact delegated child stop", ErrGenerationStopped))
					observation.done = append(observation.done, run.done)
					matched = true
					outcome.ExecutionObservation = "running"
				}
			}
			run.settlement.Unlock()
		}
		s.sessionRunsMu.Unlock()
		if supervisor != nil {
			run := supervisor
			run.targetMu.Lock()
			if run.target != nil && run.target.FrameIncarnationID == target.FrameIncarnationID && run.target.StreamUID == target.StreamUID && run.target.StreamEpoch == target.StreamEpoch &&
				run.target.InputRevision == target.InputRevision && run.target.InputMessageID == target.InputMessageID {
				allowed := matched
				if !allowed && target.RunnerAttempt == 0 && run.target.RunnerAttempt == 0 {
					var err error
					allowed, err = s.workspaceStore.DelegatedStopSignalAllowed(ctx, owner, *receipt, target, nil)
					if err != nil {
						run.targetMu.Unlock()
						return complete, err
					}
				}
				if allowed {
					run.markCanonicalStopLocked(target)
					run.stop()
					observation.done = append(observation.done, run.done)
					matched = true
				}
			}
			run.targetMu.Unlock()
		}
		if matched {
			outcome.ExecutionObservation = "running"
		}
	}
	return complete, nil
}

func (s *Server) startDelegatedStopDrainObservation(owner string, receipt workspace.DelegatedStopReceipt, observations []delegatedStopDrainObservation, complete chan struct{}) {
	// Retain captured registration pointers through their own done channels.
	// Never infer death from absence/replacement of a weak frame-ID map entry.
	// This also runs after a later target fails: an already signalled exact
	// registration still owns its cleanup and must not lose its observation.
	captured := make([]delegatedStopDrainObservation, 0, len(observations))
	for _, observation := range observations {
		if len(observation.done) > 0 {
			captured = append(captured, observation)
		}
	}
	if len(captured) > 0 {
		frozen := receipt
		frozen.Targets = append([]workspace.DelegatedStopOutcome(nil), receipt.Targets...)
		go func() { defer close(complete); s.observeDelegatedStopDrain(owner, frozen, captured) }()
	} else {
		close(complete)
	}
}

func (s *Server) observeDelegatedStopDrain(owner string, receipt workspace.DelegatedStopReceipt, observations []delegatedStopDrainObservation) {
	// Bounded join of existing execution owners, not a new scheduler.
	// Slow/unknown cleanup remains explicitly unconfirmed in the receipt.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed := false
	for _, observation := range observations {
		complete := true
		for _, done := range observation.done {
			select {
			case <-done:
			case <-ctx.Done():
				complete = false
			}
			if !complete {
				break
			}
		}
		if complete {
			receipt.Targets[observation.index].ExecutionObservation = "stopped"
			changed = true
		}
	}
	if changed {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		if _, err := s.workspaceStore.RecordDelegatedStopObservation(persistCtx, owner, receipt); err != nil {
			log.Printf("delegated Stop observation persistence failed: %v", err)
		}
	}
}

func (s *Server) observeDelegatedStop(ctx context.Context, owner string, receipt *workspace.DelegatedStopReceipt) {
	if receipt == nil {
		return
	}
	for i := range receipt.Targets {
		outcome := &receipt.Targets[i]
		if outcome.Outcome != "cancel_requested" || outcome.ExecutionObservation == "stopped" {
			continue
		}
		outcome.ExecutionObservation = "unavailable"
		s.sessionRunsMu.Lock()
		if run := s.sessionRuns[outcome.Target.FrameID]; run != nil {
			run.settlement.Lock()
			if delegatedStopMatchesRun(outcome.Target, run.transcript) {
				if allowed, err := s.workspaceStore.DelegatedStopSignalAllowed(ctx, owner, *receipt, outcome.Target, &run.transcript.Claim); err == nil && allowed {
					outcome.ExecutionObservation = "running"
				}
			}
			run.settlement.Unlock()
		}
		s.sessionRunsMu.Unlock()
	}
}
