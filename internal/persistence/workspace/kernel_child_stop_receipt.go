package workspace

import (
	"context"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

// The existing host tool may address a verified direct child without claiming
// that its legacy origin belongs to a browser-selected branch. Its host call
// identity freezes the same canonical targets and uses the same cancellation
// core; it never provides a browser fallback or a separate terminal authority.
func (s *Store) AdmitKernelChildStop(ctx context.Context, owner string, expectedScope DelegatedStopScope, childID, reason, callID string, expectedTargets ...*DelegatedStopTarget) (DelegatedStopReceipt, error) {
	if len(expectedTargets) > 1 {
		return DelegatedStopReceipt{}, ErrDelegatedStopInvalid
	}
	if strings.TrimSpace(callID) == "" || strings.TrimSpace(childID) == "" {
		return DelegatedStopReceipt{}, ErrDelegatedStopInvalid
	}
	requestID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("kernel-child-stop:"+callID+":"+childID)).String()
	var result DelegatedStopReceipt
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, _, err := delegatedScopeTx(ctx, tx, owner, expectedScope.ParentFrameID)
		if err != nil {
			return err
		}
		if scope != expectedScope {
			return ErrDelegatedStopScopeStale
		}
		existing, _, found, err := readDelegatedStopTx(ctx, tx, owner, scope, requestID)
		if err != nil {
			return err
		}
		if found {
			if len(expectedTargets) == 1 && expectedTargets[0] != nil && (existing.Receipt.Request.Target == nil || *existing.Receipt.Request.Target != *expectedTargets[0]) {
				return ErrDelegatedStopTargetStale
			}
			if existing.HostCallID != callID || existing.HostReason != reason || existing.Receipt.Request.Target.FrameID != childID {
				return ErrDelegatedStopRequestConflict
			}
			result = existing.Receipt
			result.Replayed = true
			return nil
		}
		child, found, err := readDelegatedChildTx(ctx, tx, scope.ParentFrameID, childID, owner)
		if err != nil {
			return err
		}
		if !found {
			return ErrDelegatedStopUnavailable
		}
		target, status, err := delegatedStopTargetTx(ctx, tx, owner, scope, childID)
		if err != nil {
			return err
		}
		if len(expectedTargets) == 1 && expectedTargets[0] != nil {
			expected := *expectedTargets[0]
			// The blocking delegate owns its original input, not any later prompt.
			if expected.FrameID != target.FrameID || expected.FrameIncarnationID != target.FrameIncarnationID || expected.StreamUID != target.StreamUID || expected.StreamEpoch != target.StreamEpoch || expected.InputMessageID != target.InputMessageID || expected.InputRevision != target.InputRevision {
				return ErrDelegatedStopTargetStale
			}
			if expected.RunnerAttempt == 0 && target.RunnerAttempt > 1 || expected.RunnerAttempt > 0 && target.RunnerAttempt != expected.RunnerAttempt {
				return ErrDelegatedStopTargetStale
			}
		}
		request := DelegatedStopRequest{Schema: "x-science.delegated-host-stop.v1", RequestID: requestID, Scope: scope, Mode: "child", Target: &target}
		result = DelegatedStopReceipt{Schema: "x-science.delegated-stop-receipt.v1", Request: request, AcceptedAt: s.now().UTC(), State: "stopping", Targets: []DelegatedStopOutcome{}}
		if status != "terminal" {
			descendants, err := delegatedDescendantsTx(ctx, tx, owner, child.FrameID)
			if err != nil {
				return err
			}
			for _, descendant := range descendants {
				subScope, _, err := delegatedScopeTx(ctx, tx, owner, descendant.ParentFrameID)
				if err != nil {
					return err
				}
				subTarget, _, err := delegatedStopTargetTx(ctx, tx, owner, subScope, descendant.FrameID)
				if err != nil {
					return err
				}
				outcome, err := s.cancelDelegatedTargetTx(ctx, tx, owner, subScope, subTarget)
				if err != nil {
					return err
				}
				result.Targets = append(result.Targets, outcome)
			}
		}
		outcome, err := s.cancelDelegatedTargetTx(ctx, tx, owner, scope, target)
		if err != nil {
			return err
		}
		result.Targets = append(result.Targets, outcome)
		if status == "terminal" {
			result.State = "settled"
		}
		record := delegatedStopStored{Version: 1, OwnerID: owner, HostCallID: callID, HostReason: reason, Receipt: result}
		return writeDelegatedRecord(ctx, tx, scope.ParentFrameID, delegatedRecordID("stop", owner, scope.ParentFrameIncarnationID, requestID), DelegatedStopReceiptEventType, "", record, s.now().UTC())
	})
	return result, err
}

func delegatedDescendantsTx(ctx context.Context, tx workspaceTransaction, owner, childID string) ([]KernelSupervisedChild, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE descendants(frame_id,depth) AS (
		SELECT frame_id,1 FROM kernel_child_supervision WHERE parent_frame_id=? AND owner_user_id=?
		UNION ALL SELECT child.frame_id,descendants.depth+1 FROM kernel_child_supervision child JOIN descendants ON child.parent_frame_id=descendants.frame_id WHERE child.owner_user_id=?)
		SELECT child.frame_id,child.parent_frame_id,child.root_frame_id,child.project_id,child.owner_user_id,child.tool_use_id,
		child.agent_name,child.delegate_name,child.task,child.context_summary,child.model,child.output_schema,child.status,child.output_data,child.error,child.dispatched,child.started_at,child.completed_at
		FROM descendants JOIN kernel_child_supervision child ON child.frame_id=descendants.frame_id ORDER BY descendants.depth DESC,child.frame_id`, childID, owner, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []KernelSupervisedChild
	for rows.Next() {
		child, _, err := scanKernelSupervisedChild(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, child)
	}
	return result, rows.Err()
}
