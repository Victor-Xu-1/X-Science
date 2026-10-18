package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
	"unicode/utf8"
)

func (s *Store) CompleteManualKernelExecution(ctx context.Context, input ManualKernelCompletionInput) (ManualKernelExecution, error) {
	if s == nil || s.db == nil {
		return ManualKernelExecution{}, ErrWorkspaceStoreClosed
	}
	if input.FinishedAt.IsZero() {
		return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
	}
	input.FinishedAt = input.FinishedAt.UTC()
	if input.Rejection != nil && (input.Log != nil || input.Disposition != "rejected_before_start" || !utf8.ValidString(input.Rejection.Stdout) || !utf8.ValidString(input.Rejection.Stderr)) {
		return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
	}
	var prepared *preparedExecutionLog
	logSHA := ""
	if input.Log != nil {
		record := *input.Log
		wantStatus := map[string]string{"completed": "ok", "failed": "error", "cancelled": "cancelled"}[input.Disposition]
		if wantStatus == "" || record.ExitStatus != wantStatus || record.Origin != "user" || record.ID != input.Expected.ExecutionID || record.FrameID != input.Expected.FrameID || record.KernelID != input.Expected.KernelID || record.KernelKind != input.Expected.KernelKind || record.Language != input.Expected.Request.Language || record.CondaEnv != input.Expected.Request.Environment || record.Source != input.Expected.Request.Source || record.ExecutedAt.IsZero() || record.CellIndex <= 0 {
			return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
		}
		value, err := s.prepareExecutionLog(SaveExecutionLogInput{Record: record, ExpectedOwnerID: input.Expected.OwnerID, ExpectedProjectID: input.Expected.ProjectID, ExpectedFrameIncarnationID: input.Expected.FrameIncarnationID, ExpectedRootFrameIncarnationID: input.Expected.RootIncarnationID})
		if err != nil {
			return ManualKernelExecution{}, err
		}
		prepared = &value
		logSHA, err = kernelSettlementExecutionLogSHA256(value.record)
		if err != nil {
			return ManualKernelExecution{}, err
		}
	} else if input.Disposition != "cancelled_before_start" && input.Disposition != "rejected_before_start" {
		return ManualKernelExecution{}, ErrManualKernelExecutionInvalid
	}
	raw, err := json.Marshal(struct {
		Disposition string
		FinishedAt  time.Time
		LogSHA256   string
		Rejection   *ManualKernelRejection
	}{input.Disposition, input.FinishedAt, logSHA, input.Rejection})
	if err != nil {
		return ManualKernelExecution{}, err
	}
	digest := sha256.Sum256(raw)
	resultSHA := hex.EncodeToString(digest[:])
	return s.manualKernelTransition(ctx, input.Expected, func(tx workspaceTransaction, current ManualKernelExecution) (ManualKernelExecution, error) {
		if current.State == "terminal" {
			if current.ResultSHA256 == resultSHA && current.Disposition == input.Disposition {
				return current, nil
			}
			return current, ErrManualKernelExecutionConflict
		}
		if current.State == "outcome_unknown" {
			return current, ErrManualKernelExecutionStale
		}
		if current.StateVersion != input.Expected.StateVersion {
			return current, ErrManualKernelExecutionStale
		}
		next := current
		next.State = "terminal"
		next.StateVersion++
		next.FinishedAt = &input.FinishedAt
		next.Disposition = input.Disposition
		next.ResultSHA256 = resultSHA
		if prepared == nil {
			if current.StartedAt != nil {
				return current, ErrManualKernelExecutionConflict
			}
			if input.Rejection != nil {
				diagnostic := *input.Rejection
				next.Rejection = &diagnostic
			}
			return next, s.enqueueManualKernelPrestartDone(ctx, tx, next)
		}
		if current.StartedAt != nil && !current.StartedAt.Equal(prepared.record.ExecutedAt) {
			return current, ErrManualKernelExecutionConflict
		}
		started := prepared.record.ExecutedAt.UTC()
		next.StartedAt = &started
		if current.StartedAt == nil {
			if err := s.enqueueManualKernelStart(ctx, tx, current, started); err != nil {
				return current, err
			}
		}
		if _, err := saveExecutionLogTx(ctx, tx, *prepared); err != nil {
			return current, err
		}
		next.ExecutionLogID = prepared.record.ID
		var exitCode any = 0
		if input.Disposition == "failed" {
			exitCode = -1
		} else if input.Disposition == "cancelled" {
			exitCode = nil
		}
		duration := max(int64(0), input.FinishedAt.Sub(started).Milliseconds())
		_, err := s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
			ID: "kernel-execution:" + current.ExecutionID + ":done", UserID: current.OwnerID, ProjectID: current.ProjectID, RootFrameID: current.RootFrameID, FrameID: current.FrameID, Type: "execution_cell_update",
			Payload: map[string]any{"phase": "done", "origin": "user", "language": current.Request.Language, "environment": current.Request.Environment, "kernel_target": "analysis", "cell_id": current.ExecutionID, "exit_code": exitCode, "stdout": prepared.record.Stdout, "stderr": prepared.record.Stderr, "cancelled": input.Disposition == "cancelled", "duration_ms": duration, "tool_use_id": "user-" + current.ExecutionID},
		}, manualKernelEventID(current.ExecutionID), "")
		return next, err
	})
}

func (s *Store) enqueueManualKernelPrestartDone(ctx context.Context, tx workspaceTransaction, record ManualKernelExecution) error {
	stdout, stderr := "", ""
	if record.Rejection != nil {
		stdout, stderr = record.Rejection.Stdout, record.Rejection.Stderr
	}
	var exitCode any
	if record.Disposition == "rejected_before_start" {
		exitCode = -1
	}
	_, err := s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
		ID: "kernel-execution:" + record.ExecutionID + ":done", UserID: record.OwnerID, ProjectID: record.ProjectID, RootFrameID: record.RootFrameID, FrameID: record.FrameID, Type: "execution_cell_update",
		Payload: map[string]any{"phase": "done", "origin": "user", "language": record.Request.Language, "environment": record.Request.Environment, "kernel_target": "analysis", "cell_id": record.ExecutionID, "exit_code": exitCode, "stdout": stdout, "stderr": stderr, "cancelled": record.Disposition == "cancelled_before_start", "duration_ms": 0, "tool_use_id": "user-" + record.ExecutionID},
	}, manualKernelEventID(record.ExecutionID), "")
	return err
}
