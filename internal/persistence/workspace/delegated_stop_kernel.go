package workspace

import (
	"context"
	"time"
	transcriptstore "x-science/internal/persistence/transcript"
)

const delegatedKernelOperationScope = `owner_user_id=? AND root_frame_id=? AND root_frame_incarnation_id=? AND frame_id=? AND frame_incarnation_id=?
 AND stream_uid=? AND runner_attempt=? AND admitted_input_revision=?`

func delegatedKernelOperationArgs(owner string, scope DelegatedStopScope, target DelegatedStopTarget) []any {
	return []any{owner, scope.RootFrameID, scope.RootFrameIncarnationID, target.FrameID, target.FrameIncarnationID, target.StreamUID, target.RunnerAttempt, target.InputRevision}
}

func requestDetachedDelegatedCancellationTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, owner string, scope DelegatedStopScope, target DelegatedStopTarget, now time.Time) error {
	if target.RunnerAttempt == 0 {
		return nil
	}
	return requestDetachedKernelCancellationQueryTx(ctx, tx, `SELECT execution_id FROM kernel_detached_executions
  WHERE state IN ('accepted','dispatch_committed','started') AND operation_id IN (
   SELECT operation_id FROM kernel_local_operations WHERE state='started' AND `+delegatedKernelOperationScope+`
  ) ORDER BY execution_id`, delegatedKernelOperationArgs(owner, scope, target), now)
}

func delegatedKernelExecutionUnconfirmedTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, target DelegatedStopTarget) (bool, error) {
	if target.RunnerAttempt == 0 {
		return false, nil
	}
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM kernel_local_operations WHERE state IN ('prepared','started','outcome_unknown') AND `+delegatedKernelOperationScope+`)`,
		delegatedKernelOperationArgs(owner, scope, target)...).Scan(&pending)
	return pending, err
}

// Shared durable executor cancellation update; query selection is owned by its
// caller's already validated root or exact-claim transaction.
func requestDetachedKernelCancellationQueryTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, query string, args []any, now time.Time) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		current, found, err := getDetachedKernelExecutionQuery(ctx, tx, id)
		if err != nil {
			return err
		}
		if !found || current.CancelRequestID != "" ||
			(current.State != DetachedKernelExecutionStateAccepted &&
				current.State != DetachedKernelExecutionStateDispatchCommitted &&
				current.State != DetachedKernelExecutionStateStarted) {
			return ErrDetachedKernelExecutionConflict
		}
		cancelRequestID := "frame-cancel:" + current.ExecutionID
		if _, err := updateDetachedKernelExecutionTx(ctx, tx, current,
			`state=?,state_version=state_version+1,cancel_request_id=?,cancel_requested_at=?,updated_at=?`,
			DetachedKernelExecutionStateCancelRequested, cancelRequestID,
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}
