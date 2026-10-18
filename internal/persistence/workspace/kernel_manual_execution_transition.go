package workspace

import (
	"context"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) manualKernelTransition(ctx context.Context, expected ManualKernelExecution, apply func(workspaceTransaction, ManualKernelExecution) (ManualKernelExecution, error)) (ManualKernelExecution, error) {
	if s == nil || s.db == nil {
		return ManualKernelExecution{}, ErrWorkspaceStoreClosed
	}
	if err := validateManualKernelExecution(expected); err != nil {
		return ManualKernelExecution{}, err
	}
	var result ManualKernelExecution
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := validateManualKernelScope(ctx, tx, expected.ManualKernelBinding); err != nil {
			return err
		}
		current, raw, found, err := readManualKernelExecution(ctx, tx, expected.ExecutionID)
		if err != nil {
			return err
		}
		if !found {
			return ErrManualKernelExecutionStale
		}
		if current.ManualKernelBinding != expected.ManualKernelBinding || current.Request != expected.Request || current.RequestSHA256 != expected.RequestSHA256 {
			return ErrManualKernelExecutionConflict
		}
		next, err := apply(tx, current)
		if err != nil {
			return err
		}
		if next.StateVersion == current.StateVersion {
			result = current
			return nil
		}
		if current.StateVersion != expected.StateVersion || next.StateVersion != current.StateVersion+1 {
			return ErrManualKernelExecutionStale
		}
		if err := updateManualKernelExecution(ctx, tx, current, raw, next); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, err
}

func (s *Store) MarkManualKernelExecutionStarted(ctx context.Context, expected ManualKernelExecution, startedAt time.Time) (ManualKernelExecution, error) {
	if startedAt.IsZero() {
		return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
	}
	startedAt = startedAt.UTC()
	return s.manualKernelTransition(ctx, expected, func(tx workspaceTransaction, current ManualKernelExecution) (ManualKernelExecution, error) {
		if current.StartedAt != nil {
			if current.StartedAt.Equal(startedAt) {
				return current, nil
			}
			return current, ErrManualKernelExecutionConflict
		}
		if current.State != "queued" && current.State != "cancel_requested" {
			return current, ErrManualKernelExecutionStale
		}
		if current.StateVersion != expected.StateVersion {
			return current, ErrManualKernelExecutionStale
		}
		next := current
		next.StartedAt = &startedAt
		next.StateVersion++
		if next.State == "queued" {
			next.State = "started"
		}
		return next, s.enqueueManualKernelStart(ctx, tx, current, startedAt)
	})
}

func (s *Store) enqueueManualKernelStart(ctx context.Context, tx workspaceTransaction, current ManualKernelExecution, startedAt time.Time) error {
	_, err := s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
		ID: "kernel-execution:" + current.ExecutionID + ":start", UserID: current.OwnerID, ProjectID: current.ProjectID, RootFrameID: current.RootFrameID, FrameID: current.FrameID, Type: "execution_cell_update",
		Payload: map[string]any{"phase": "start", "origin": "user", "language": current.Request.Language, "environment": current.Request.Environment, "kernel_target": "analysis", "source": current.Request.Source, "started_at": startedAt.Format(time.RFC3339Nano), "tool_use_id": "user-" + current.ExecutionID},
	}, manualKernelEventID(current.ExecutionID), "")
	return err
}

func (s *Store) RequestManualKernelExecutionCancellation(ctx context.Context, expected ManualKernelExecution) (ManualKernelExecution, error) {
	return s.manualKernelTransition(ctx, expected, func(_ workspaceTransaction, current ManualKernelExecution) (ManualKernelExecution, error) {
		if current.State == "terminal" || current.CancelRequestedAt != nil {
			return current, nil
		}
		if current.State != "queued" && current.State != "started" {
			return current, ErrManualKernelExecutionStale
		}
		now := s.now().UTC()
		next := current
		next.State = "cancel_requested"
		next.CancelRequestedAt = &now
		next.StateVersion++
		return next, nil
	})
}

// Unknown records retain execution identity and never authorize another run.
// A different server boot is not physical-stop evidence.
func (s *Store) MarkManualKernelExecutionUnknown(ctx context.Context, expected ManualKernelExecution, reason string) (ManualKernelExecution, error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 256 {
		return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
	}
	return s.manualKernelTransition(ctx, expected, func(_ workspaceTransaction, current ManualKernelExecution) (ManualKernelExecution, error) {
		if current.State == "outcome_unknown" && current.Disposition == reason {
			return current, nil
		}
		if current.State == "terminal" || current.State == "outcome_unknown" {
			return current, ErrManualKernelExecutionStale
		}
		next := current
		next.State = "outcome_unknown"
		next.Disposition = reason
		next.StateVersion++
		return next, nil
	})
}
