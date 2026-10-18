package server

import (
	"context"
	"errors"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func contextCompactionTerminalReceipt(receipt *transcriptstore.ContextCompactionReceipt) bool {
	return receipt != nil && receipt.Accepted && (receipt.State == "committed" || receipt.State == "cancelled" || receipt.State == "failed")
}

func (s *Server) observeContextCompaction(receipt *transcriptstore.ContextCompactionReceipt) {
	if receipt == nil || !receipt.Accepted {
		return
	}
	s.sessionRunsMu.Lock()
	defer s.sessionRunsMu.Unlock()
	run := s.sessionRuns[receipt.FrameID]
	terminal := receipt.State == "committed" || receipt.State == "cancelled" || receipt.State == "failed"
	if run == nil {
		// Durable fencing permits a new admission, not a claim that an old
		// process physically stopped. Keep the missing-owner observation honest.
		receipt.ExecutionObservation = "unavailable"
		receipt.CanSubmitNextInput = terminal && receipt.CanSubmitNextInput
		return
	}
	run.settlement.Lock()
	defer run.settlement.Unlock()
	authority := run.transcript
	if authority == nil || authority.Claim.StreamUID != receipt.Request.Expected.StreamUID || authority.Claim.Attempt != receipt.Attempt || authority.Claim.ClaimedInputRevision != receipt.InputRevision {
		receipt.CanSubmitNextInput = false
		return
	}
	if terminal {
		receipt.ExecutionObservation = "draining"
	} else {
		receipt.ExecutionObservation = "active"
		if receipt.State != "recovery_needed" {
			receipt.State = "compacting"
		}
	}
	receipt.CanSubmitNextInput = false
}

func (s *Server) cancelContextCompactionAndDrain(ctx context.Context, owner, frame string, request transcriptstore.ContextCompactionRequest) (transcriptstore.ContextCompactionReceipt, error) {
	return s.cancelContextCompaction(ctx, owner, frame, request, true)
}

func (s *Server) cancelContextCompaction(ctx context.Context, owner, frame string, request transcriptstore.ContextCompactionRequest, waitForOwner bool) (transcriptstore.ContextCompactionReceipt, error) {
	s.sessionRunsMu.Lock()
	owned := s.sessionRuns[frame]
	s.sessionRunsMu.Unlock()
	var receipt transcriptstore.ContextCompactionReceipt
	var done <-chan struct{}
	err := s.commitScopedSessionRunCancellation([]string{frame}, func(authorities map[string]*transcriptRunnerAuthority) ([]string, error) {
		var err error
		receipt, err = s.workspaceStore.CancelContextCompaction(ctx, owner, frame, request)
		if err != nil {
			return nil, err
		}
		authority := authorities[frame]
		if owned == nil || authority == nil || authority != owned.transcript || authority.Claim.StreamUID != request.Expected.StreamUID ||
			authority.Claim.Attempt != receipt.Attempt || authority.Claim.ClaimedInputRevision != receipt.InputRevision {
			return nil, nil
		}
		done = owned.done
		// commitScopedSessionRunCancellation already owns this exact run's
		// settlement lock. Its original cleanup alone may announce the drain.
		owned.contextCompaction = &request
		if receipt.State == "cancelled" {
			return []string{frame}, nil
		}
		return nil, nil
	})
	if err != nil {
		return receipt, err
	}
	s.signalTranscriptWebDelivery()
	if done != nil && waitForOwner {
		wait, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		select {
		case <-done:
		case <-wait.Done():
		}
	}
	readCtx, stopRead := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopRead()
	receipt, err = s.transcriptStore.ReadContextCompaction(readCtx, owner, frame, request)
	if err != nil {
		return receipt, err
	}
	s.observeContextCompaction(&receipt)
	return receipt, nil
}

func (s *Server) preemptContextCompactionForInput(ctx context.Context, owner, frame string) error {
	if s.transcriptStore == nil {
		return nil
	}
	snapshot, err := s.transcriptStore.ContextCompactionSnapshot(ctx, owner, frame)
	if errors.Is(err, transcriptstore.ErrContextCompactionUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	if snapshot.Operation == nil {
		return nil
	}
	receipt := *snapshot.Operation
	s.observeContextCompaction(&receipt)
	if receipt.CanSubmitNextInput {
		return nil
	}
	receipt, err = s.cancelContextCompactionAndDrain(ctx, owner, frame, receipt.Request)
	if err != nil {
		return err
	}
	if !receipt.CanSubmitNextInput {
		return transcriptstore.ErrContextCompactionBusy
	}
	return nil
}

func (s *Server) drainContextCompactionForQueuedInput(ctx context.Context, owner, frame string) error {
	snapshot, err := s.transcriptStore.ContextCompactionSnapshot(ctx, owner, frame)
	if errors.Is(err, transcriptstore.ErrContextCompactionUnavailable) {
		return nil
	}
	if err != nil || snapshot.Operation == nil {
		return err
	}
	receipt, err := s.cancelContextCompaction(ctx, owner, frame, snapshot.Operation.Request, false)
	if err != nil {
		return err
	}
	// This is delivery of the already-owned queued input, not permission for
	// an additional input. Its own queued row must not deadlock its drain.
	if receipt.ExecutionObservation == "active" || receipt.ExecutionObservation == "draining" {
		return transcriptstore.ErrContextCompactionBusy
	}
	return nil
}
