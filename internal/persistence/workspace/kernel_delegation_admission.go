package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

func delegationRequestDigest(requests []KernelDelegateRequest) (string, error) {
	raw, err := json.Marshal(requests)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// Capture is called once for the existing host-call identity before admission.
// A replay retains the first branch fence, so a delayed old call cannot become
// a new delegation after Stop. Only a new explicit call obtains a new fence.
func (s *Store) CaptureKernelDelegationAdmission(ctx context.Context, parent, owner, callID, outerCallID string, claim transcriptstore.RunnerClaim, requests []KernelDelegateRequest) (*KernelDelegationAdmission, error) {
	if parent == "" || owner == "" || callID == "" || outerCallID == "" {
		return nil, ErrDelegatedStopInvalid
	}
	digest, err := delegationRequestDigest(requests)
	if err != nil {
		return nil, err
	}
	var result KernelDelegationAdmission
	err = transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		stream, err := tx.ValidateLiveRunnerClaim(ctx, claim)
		if err != nil {
			return err
		}
		if stream.FrameID != parent || stream.OwnerID != owner {
			return ErrDelegatedStopScopeStale
		}
		scope, _, err := delegatedScopeTx(ctx, tx, owner, parent)
		if err != nil {
			return err
		}
		id := delegatedRecordID("admission", owner, scope.ParentFrameIncarnationID, callID)
		var existing delegationAdmissionRecord
		_, found, err := readDelegatedRecord(ctx, tx, parent, id, delegationAdmissionEventType, &existing)
		if err != nil {
			return err
		}
		result = KernelDelegationAdmission{ID: id, Claim: claim}
		if found {
			if existing.Version != 1 || existing.OwnerID != owner || existing.Scope != scope || existing.ToolUseID != callID || existing.OuterToolUseID != outerCallID || existing.RequestSHA256 != digest || existing.StreamUID != stream.UID || existing.StreamEpoch != stream.Epoch || existing.RunnerAttempt != claim.Attempt {
				return ErrDelegatedStopRequestConflict
			}
			return nil
		}
		var branch DelegatedStopBranch
		var sourceID, sourceAttempt int64
		err = tx.QueryRowContext(ctx, `SELECT branch.active_branch_id,branch.generation,item.started_event_id,event.runner_attempt
			FROM transcript_tool_call_items item JOIN transcript_tool_call_batches batch ON batch.batch_id=item.batch_id
			JOIN transcript_branch_state branch ON branch.stream_uid=batch.stream_uid AND branch.active_branch_id=batch.branch_id AND branch.generation=batch.branch_generation
			JOIN transcript_events event ON event.stream_uid=item.stream_uid AND event.event_id=item.started_event_id
			JOIN transcript_branch_events member ON member.stream_uid=item.stream_uid AND member.branch_id=branch.active_branch_id AND member.event_id=event.event_id
			WHERE item.stream_uid=? AND batch.owner_user_id=? AND item.tool_call_id=? AND item.ordinal=batch.next_ordinal AND item.state IN ('running','waiting')`, stream.UID, owner, outerCallID).Scan(&branch.BranchID, &branch.Generation, &sourceID, &sourceAttempt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDelegatedStopOriginUnavailable
		}
		if err != nil {
			return err
		}
		if sourceAttempt != claim.Attempt || sourceID <= 0 {
			return ErrDelegatedStopOriginUnavailable
		}
		fence, _, _, err := delegationFenceTx(ctx, tx, owner, scope, stream.UID, stream.Epoch, branch.BranchID)
		if err != nil {
			return err
		}
		record := delegationAdmissionRecord{Version: 1, OwnerID: owner, Scope: scope, Branch: branch, StreamUID: stream.UID, StreamEpoch: stream.Epoch,
			InputRevision: claim.ClaimedInputRevision, SourceEventID: sourceID, RunnerAttempt: claim.Attempt, ToolUseID: callID, OuterToolUseID: outerCallID, RequestSHA256: digest, FenceGeneration: fence.Generation}
		record.VisibleFenceGenerations, err = delegationVisibleFencesTx(ctx, tx, record)
		if err != nil {
			return err
		}
		return writeDelegatedRecord(ctx, tx, parent, id, delegationAdmissionEventType, "", record, s.now().UTC())
	})
	return &result, err
}

func (s *Store) validateDelegationAdmissionTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, input CreateKernelDelegatesInput) ([]KernelSupervisedChild, bool, error) {
	if input.Admission == nil {
		return nil, false, nil
	}
	var record delegationAdmissionRecord
	_, found, err := readDelegatedRecord(ctx, tx, input.ParentFrameID, input.Admission.ID, delegationAdmissionEventType, &record)
	if err != nil {
		return nil, false, err
	}
	digest, err := delegationRequestDigest(input.Requests)
	if err != nil {
		return nil, false, err
	}
	scope, _, err := delegatedScopeTx(ctx, tx, input.OwnerUserID, input.ParentFrameID)
	if err != nil {
		return nil, false, err
	}
	if !found || record.Version != 1 || record.OwnerID != input.OwnerUserID || record.Scope != scope || record.RequestSHA256 != digest || record.ToolUseID != input.ToolUseID {
		return nil, false, ErrDelegatedStopRequestConflict
	}
	// Accepted replay precedes mutable lease/branch checks. It can only return
	// the original children; their own canonical cancellation still fences run.
	if len(record.ChildIDs) > 0 {
		children := make([]KernelSupervisedChild, 0, len(record.ChildIDs))
		for _, id := range record.ChildIDs {
			child, found, err := readDelegatedChildTx(ctx, tx, input.ParentFrameID, id, input.OwnerUserID)
			if err != nil {
				return nil, false, err
			}
			if !found {
				return nil, false, ErrDelegatedStopUnavailable
			}
			children = append(children, child)
		}
		return children, true, nil
	}
	stream, err := tx.ValidateLiveRunnerClaim(ctx, input.Admission.Claim)
	if err != nil {
		return nil, false, err
	}
	if stream.UID != record.StreamUID || stream.Epoch != record.StreamEpoch || input.Admission.Claim.Attempt != record.RunnerAttempt || input.Admission.Claim.ClaimedInputRevision != record.InputRevision {
		return nil, false, ErrDelegatedStopTargetStale
	}
	var branch string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`, stream.UID).Scan(&branch, &generation); err != nil {
		return nil, false, err
	}
	if branch != record.Branch.BranchID || generation != record.Branch.Generation {
		return nil, false, ErrDelegatedStopBranchStale
	}
	fence, _, _, err := delegationFenceTx(ctx, tx, record.OwnerID, scope, stream.UID, stream.Epoch, branch)
	if err != nil {
		return nil, false, err
	}
	if fence.Generation != record.FenceGeneration {
		return nil, false, ErrDelegatedStopTargetStale
	}
	visible, err := delegationVisibleFencesTx(ctx, tx, record)
	if err != nil {
		return nil, false, err
	}
	if record.VisibleFenceGenerations == nil {
		return nil, false, ErrDelegatedStopOriginUnavailable
	}
	for branch, generation := range visible {
		if record.VisibleFenceGenerations[branch] != generation {
			return nil, false, ErrDelegatedStopTargetStale
		}
	}
	return nil, false, nil
}

func (s *Store) completeDelegationAdmissionTx(ctx context.Context, tx workspaceTransaction, input CreateKernelDelegatesInput, children []KernelSupervisedChild) error {
	if input.Admission == nil {
		return nil
	}
	var record delegationAdmissionRecord
	raw, found, err := readDelegatedRecord(ctx, tx, input.ParentFrameID, input.Admission.ID, delegationAdmissionEventType, &record)
	if err != nil {
		return err
	}
	if !found || len(record.ChildIDs) > 0 {
		return ErrDelegatedStopRequestConflict
	}
	for _, child := range children {
		record.ChildIDs = append(record.ChildIDs, child.FrameID)
	}
	return writeDelegatedRecord(ctx, tx, input.ParentFrameID, input.Admission.ID, delegationAdmissionEventType, raw, record, s.now().UTC())
}

func readDelegatedChildTx(ctx context.Context, tx workspaceTransaction, parent, child, owner string) (KernelSupervisedChild, bool, error) {
	return scanKernelSupervisedChild(tx.QueryRowContext(ctx, `SELECT frame_id,parent_frame_id,root_frame_id,project_id,owner_user_id,tool_use_id,
		agent_name,delegate_name,task,context_summary,model,output_schema,status,output_data,error,dispatched,started_at,completed_at
		FROM kernel_child_supervision WHERE frame_id=? AND parent_frame_id=? AND owner_user_id=?`, child, parent, owner))
}
