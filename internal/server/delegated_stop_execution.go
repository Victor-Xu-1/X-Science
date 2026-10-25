package server

import (
	"context"
	"fmt"
	"time"
	workspace "x-science/internal/persistence/workspace"
)

func delegatedStopMatchesRun(target workspace.DelegatedStopTarget, authority *transcriptRunnerAuthority) bool {
	return authority != nil && authority.Stream.FrameID == target.FrameID && authority.Stream.UID == target.StreamUID && authority.Stream.Epoch == target.StreamEpoch &&
		authority.Claim.Attempt == target.RunnerAttempt && authority.Claim.ClaimedInputRevision == target.InputRevision
}

type delegatedStopDrainObservation struct {
	index         int
	registrations []delegatedStopRegistration
}

func (s *Server) signalDelegatedStop(ctx context.Context, owner string, receipt *workspace.DelegatedStopReceipt) (<-chan struct{}, error) {
	complete := make(chan struct{})
	if receipt == nil {
		close(complete)
		return complete, nil
	}
	// Retain actual signal/subscription ownership until its captured cleanup
	// callbacks have either stayed attached or handed over a finite writer.
	finishSignal, err := s.beginAcceptedStopContinuation()
	if err != nil {
		close(complete)
		return complete, err
	}
	defer finishSignal()
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
					observation.registrations = append(observation.registrations, delegatedStopRegistration{run.done, &run.stopCompletion})
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
					observation.registrations = append(observation.registrations, delegatedStopRegistration{run.done, &run.stopCompletion})
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
	// Seal every exact owner set before subscribing: an already-finished first
	// registration cannot acknowledge a target with another owner still alive.
	frozen := freezeDelegatedStopReceipt(receipt)
	bindings := make([]*delegatedStopCompletion, 0, len(observations))
	for _, observation := range observations {
		if len(observation.registrations) > 0 {
			bindings = append(bindings, &delegatedStopCompletion{server: s, owner: owner, receipt: frozen,
				index: observation.index, registrations: append([]delegatedStopRegistration(nil), observation.registrations...)})
		}
	}
	for _, binding := range bindings {
		binding.subscribe()
	}
	if len(bindings) == 0 {
		close(complete)
		return
	}
	go func() { defer close(complete); s.observeDelegatedStopDrain(bindings) }()
}

func (s *Server) observeDelegatedStopDrain(bindings []*delegatedStopCompletion) {
	// The caller still has a bounded observation window. Registration-lifetime
	// completion retains the same acknowledgement after this observer returns.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, binding := range bindings {
		for _, registration := range binding.registrations {
			select {
			case <-registration.done:
			case <-ctx.Done():
			}
		}
		// Existing raw done-channel consumers share the same retained proof.
		binding.enqueueIfDrained()
	}
	if ctx.Err() == nil && len(bindings) > 0 {
		// Preserve bounded done/readback semantics without making cleanup or
		// the signal defer wait for storage. Write failures retain their proof.
		_, _ = s.flushDelegatedStopProofs(ctx, bindings[0].owner, bindings[0].receipt)
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
