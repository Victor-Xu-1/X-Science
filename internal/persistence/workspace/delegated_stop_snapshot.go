package workspace

import (
	"context"
	"database/sql"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) DelegatedStopSnapshot(ctx context.Context, owner, parent, branchID string) (DelegatedStopSnapshot, error) {
	if s == nil || s.db == nil {
		return DelegatedStopSnapshot{}, ErrWorkspaceStoreClosed
	}
	if branchID != "" && !transcriptstore.IsValidBranchID(branchID) {
		return DelegatedStopSnapshot{}, ErrDelegatedStopInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DelegatedStopSnapshot{}, err
	}
	defer tx.Rollback()
	result, err := delegatedStopSnapshotTx(ctx, tx, owner, parent, branchID)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func delegatedStopSnapshotTx(ctx context.Context, tx workspaceTransaction, owner, parent, branchID string) (DelegatedStopSnapshot, error) {
	result := DelegatedStopSnapshot{Schema: "x-science.delegated-stop-snapshot.v1", Targets: []DelegatedStopCandidate{}}
	scope, _, err := delegatedScopeTx(ctx, tx, owner, parent)
	if err != nil {
		return result, err
	}
	result.Scope = scope
	uid, epoch, revision, err := delegatedStreamTx(ctx, tx, owner, parent)
	if err != nil {
		return result, err
	}
	var active string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`, uid).Scan(&active, &generation); err != nil {
		return result, err
	}
	if branchID == "" {
		branchID = active
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_branches WHERE stream_uid=? AND branch_id=?)`, uid, branchID).Scan(&exists); err != nil {
		return result, err
	}
	if !exists {
		return result, ErrDelegatedStopBranchStale
	}
	result.Branch = DelegatedStopBranch{BranchID: branchID, Generation: generation}
	result.ParentInputRevision = revision
	fence, _, _, err := delegationFenceTx(ctx, tx, owner, scope, uid, epoch, branchID)
	if err != nil {
		return result, err
	}
	result.FenceGeneration = fence.Generation
	var supervisionExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='kernel_child_supervision')`).Scan(&supervisionExists); err != nil {
		return result, err
	}
	if !supervisionExists {
		return result, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT child.frame_id FROM kernel_child_supervision child
		JOIN frames frame ON frame.id=child.frame_id AND frame.parent_frame_id=child.parent_frame_id
		WHERE child.parent_frame_id=? AND child.owner_user_id=? ORDER BY child.started_at,child.frame_id`, parent, owner)
	if err != nil {
		return result, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for _, id := range ids {
		candidate, err := delegatedStopCandidateTx(ctx, tx, owner, scope, uid, epoch, branchID, id)
		if err != nil {
			return result, err
		}
		result.Targets = append(result.Targets, candidate)
		result.CanStopBranch = result.CanStopBranch || candidate.InSelectedBranch && candidate.CanStop
	}
	return result, nil
}

func delegatedStopCandidateTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, parentStream string, parentEpoch int64, branch, childID string) (DelegatedStopCandidate, error) {
	result := DelegatedStopCandidate{FrameID: childID, Origin: "legacy-unavailable", Status: "unavailable"}
	target, status, err := delegatedStopTargetTx(ctx, tx, owner, scope, childID)
	if errors.Is(err, ErrDelegatedStopOriginUnavailable) || errors.Is(err, ErrDelegatedStopTargetStale) {
		result.Reason = err.Error()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Target, result.Status, result.CanStop = &target, status, status != "terminal"
	var origin delegationOriginRecord
	_, found, err := readDelegatedRecord(ctx, tx, childID, delegatedRecordID("origin", childID, target.FrameIncarnationID), delegationOriginEventType, &origin)
	if err != nil {
		return result, err
	}
	if !found {
		return result, nil
	}
	if origin.Version != 1 || origin.OwnerID != owner || origin.Scope != scope || origin.InitialTarget.FrameID != childID || origin.InitialTarget.FrameIncarnationID != target.FrameIncarnationID {
		result.CanStop, result.Reason = false, ErrDelegatedStopScopeStale.Error()
		return result, nil
	}
	if origin.AdmissionID == "" {
		return result, nil
	}
	var admission delegationAdmissionRecord
	_, found, err = readDelegatedRecord(ctx, tx, scope.ParentFrameID, origin.AdmissionID, delegationAdmissionEventType, &admission)
	if err != nil {
		return result, err
	}
	if !found || admission.Version != 1 || admission.Scope != scope || admission.OwnerID != owner || admission.StreamUID != parentStream || admission.StreamEpoch != parentEpoch || admission.ToolUseID != origin.ToolUseID {
		result.CanStop, result.Reason = false, ErrDelegatedStopOriginUnavailable.Error()
		return result, nil
	}
	listed := false
	for _, id := range admission.ChildIDs {
		listed = listed || id == childID
	}
	if !listed {
		result.CanStop, result.Reason = false, ErrDelegatedStopOriginUnavailable.Error()
		return result, nil
	}
	result.Origin = "verified"
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_branch_events WHERE stream_uid=? AND branch_id=? AND event_id=?)`, parentStream, branch, admission.SourceEventID).Scan(&result.InSelectedBranch)
	return result, err
}

// Exact current input proof is shared by the browser and host.stop_child. A
// later queued followup is never silently discarded by cancelling its ancestor.
func delegatedStopTargetTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, childID string) (DelegatedStopTarget, string, error) {
	return delegatedStopTargetWithPendingTx(ctx, tx, owner, scope, childID, false)
}

func delegatedStopTargetWithPendingTx(ctx context.Context, tx workspaceTransaction, owner string, scope DelegatedStopScope, childID string, dispatch bool) (DelegatedStopTarget, string, error) {
	target := DelegatedStopTarget{FrameID: childID}
	var status string
	err := tx.QueryRowContext(ctx, `SELECT child.incarnation_id,child.status FROM frames child
		JOIN projects p ON p.id=child.project_id JOIN frames root ON root.id=child.root_frame_id
		WHERE child.id=? AND child.parent_frame_id=? AND child.root_frame_id=? AND root.incarnation_id=? AND p.user_id=?`, childID, scope.ParentFrameID, scope.RootFrameID, scope.RootFrameIncarnationID, owner).Scan(&target.FrameIncarnationID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return target, "", ErrDelegatedStopUnavailable
	}
	if err != nil {
		return target, "", err
	}
	target.StreamUID, target.StreamEpoch, target.InputRevision, err = delegatedStreamTx(ctx, tx, owner, childID)
	if err != nil {
		return target, "", err
	}
	var queued bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM queued_user_messages WHERE frame_id=? AND state IN ('queued','delivering'))
		OR EXISTS(SELECT 1 FROM kernel_child_messages WHERE frame_id=? AND consumed_at IS NULL)`, childID, childID).Scan(&queued)
	if err != nil {
		return target, "", err
	}
	if queued && !dispatch {
		return target, "", ErrDelegatedStopTargetStale
	}
	var claimedRevision int64
	var runtimeStatus string
	err = tx.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision,status FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, target.StreamUID).Scan(&target.RunnerAttempt, &claimedRevision, &runtimeStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return target, "", err
	}
	if runtimeStatus == "running" && claimedRevision != target.InputRevision && !dispatch {
		return target, "", ErrDelegatedStopTargetStale
	}
	if runtimeStatus != "running" && claimedRevision < target.InputRevision {
		target.RunnerAttempt = 0
	}
	err = tx.QueryRowContext(ctx, `SELECT event.client_message_id FROM transcript_input_admissions admission
		JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
		JOIN transcript_branch_state branch ON branch.stream_uid=admission.stream_uid
		JOIN transcript_branch_events member ON member.stream_uid=admission.stream_uid AND member.branch_id=branch.active_branch_id AND member.event_id=event.event_id
		WHERE admission.stream_uid=? AND admission.input_revision<=? AND admission.source_kind='event' AND admission.input_kind='message' AND admission.classification_version=1
		ORDER BY admission.input_revision DESC LIMIT 1`, target.StreamUID, target.InputRevision).Scan(&target.InputMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return target, "", ErrDelegatedStopOriginUnavailable
	}
	if err != nil {
		return target, "", err
	}
	if isKernelChildTerminalStatus(status) && runtimeStatus != "running" {
		return target, "terminal", nil
	}
	if status == "awaiting_user_response" || status == "awaiting_plan_approval" {
		return target, "awaiting_input", nil
	}
	return target, "running", nil
}
