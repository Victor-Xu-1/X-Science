package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

// SealedFailedArtifactInput is an in-memory proof request from the canonical
// original-candidate loader. It creates no persisted authority or new schema.
type SealedFailedArtifactInput struct {
	Projection          ProjectionSnapshot
	OwnerID             string
	Epoch               int64
	FailedAttempt       int64
	InputRevision       int64
	CorrectionEventID   int64
	TerminalEventID     int64
	CorrectionCondition *RunnerCorrectionCondition
}

type SealedFailedArtifactSnapshot struct {
	Snapshot        ArtifactCommitSnapshot
	InputReferences []ArtifactReference
}

// SealedFailedArtifactCommitSnapshot reads only a genuine failed attempt's
// bound ledger. A source event is not a commit timestamp. Unbound/late material
// makes the historical selection unknown instead of being silently omitted.
func (r *Repository) SealedFailedArtifactCommitSnapshot(ctx context.Context, input SealedFailedArtifactInput) (SealedFailedArtifactSnapshot, bool, error) {
	if r == nil || r.db == nil {
		return SealedFailedArtifactSnapshot{}, false, ErrSchemaUnavailable
	}
	condition := input.CorrectionCondition
	if input.FailedAttempt <= 0 || input.CorrectionEventID <= 0 || input.TerminalEventID <= 0 ||
		input.Epoch <= 0 || input.InputRevision <= 0 || condition == nil || condition.Validate() != nil ||
		condition.Reference == nil || len(condition.Reference.InvalidScientificArtifacts) == 0 {
		return SealedFailedArtifactSnapshot{}, false, ErrEventConflict
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	_, _, current, err := projectionSnapshotTx(ctx, tx, input.Projection.StreamUID, input.OwnerID)
	if err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	if current != input.Projection {
		return SealedFailedArtifactSnapshot{}, false, ErrBranchStateStale
	}
	var epoch, revision int64
	if err := tx.QueryRowContext(ctx, `SELECT epoch,input_revision FROM transcript_streams WHERE stream_uid=?`, current.StreamUID).Scan(&epoch, &revision); err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	if epoch != input.Epoch || revision != input.InputRevision {
		return SealedFailedArtifactSnapshot{}, false, ErrClaimStale
	}
	terminal, correction, proven, err := sealedFailedArtifactBoundary(ctx, tx, input)
	if err != nil || !proven {
		return SealedFailedArtifactSnapshot{}, false, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.runner_attempt,c.source_event_id,c.ordinal,c.artifact_id,c.version_id,c.relation,c.created_at,
			member.ordinal,source.publication_seq,bound.publication_seq,bound_member.branch_id,
			origin.claimed_input_revision,
			CASE WHEN tombstone.version_id IS NOT NULL THEN 'deleted'
				WHEN project.id IS NULL OR artifact.id IS NULL OR version.id IS NULL THEN 'missing'
				ELSE 'available' END
		FROM transcript_artifact_commits c
		JOIN transcript_branch_events member ON member.stream_uid=c.stream_uid
			AND member.event_id=c.source_event_id AND member.branch_id=?
		JOIN transcript_events source ON source.stream_uid=c.stream_uid AND source.event_id=c.source_event_id
		JOIN transcript_runner_attempts origin ON origin.stream_uid=c.stream_uid AND origin.attempt=c.runner_attempt
		JOIN transcript_streams stream ON stream.stream_uid=c.stream_uid
		LEFT JOIN transcript_events bound ON bound.stream_uid=c.stream_uid AND bound.event_id=c.bound_event_id
		LEFT JOIN transcript_branch_events bound_member ON bound_member.stream_uid=c.stream_uid
			AND bound_member.event_id=c.bound_event_id AND bound_member.branch_id=?
		LEFT JOIN projects project ON project.id=stream.project_id AND project.user_id=stream.owner_id
		LEFT JOIN artifacts artifact ON artifact.id=c.artifact_id AND artifact.project_id=project.id
		LEFT JOIN artifact_versions version ON version.id=c.version_id AND version.artifact_id=c.artifact_id
		LEFT JOIN artifact_version_tombstones tombstone ON tombstone.artifact_id=c.artifact_id
			AND tombstone.version_id=c.version_id AND tombstone.owner_id=stream.owner_id AND tombstone.project_id=stream.project_id
		WHERE c.stream_uid=? AND c.runner_attempt<=?
		ORDER BY member.ordinal,c.ordinal,c.version_id`, current.BranchID, current.BranchID, current.StreamUID, input.FailedAttempt)
	if err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	defer rows.Close()
	all, owningInput := map[string]sealedArtifactHead{}, map[string]sealedArtifactHead{}
	for rows.Next() {
		var head sealedArtifactHead
		var sourcePublication, originRevision int64
		var boundPublication sql.NullInt64
		var boundBranch sql.NullString
		var relation, availability string
		ref := &head.reference
		if err := rows.Scan(&ref.RunnerAttempt, &ref.SourceEventID, &ref.Ordinal, &ref.ArtifactID, &ref.VersionID,
			&relation, &ref.CreatedAt, &head.branchOrdinal, &sourcePublication, &boundPublication, &boundBranch,
			&originRevision, &availability); err != nil {
			return SealedFailedArtifactSnapshot{}, false, schemaError(err)
		}
		if !boundPublication.Valid || !boundBranch.Valid || boundBranch.String != current.BranchID ||
			boundPublication.Int64 > terminal || sourcePublication > correction {
			return SealedFailedArtifactSnapshot{}, false, nil
		}
		ref.StreamUID, ref.Relation, ref.Availability = current.StreamUID, ArtifactRelation(relation), ArtifactAvailability(availability)
		key := ref.ArtifactID + "\x00" + relation
		all[key] = head
		if originRevision == input.InputRevision {
			owningInput[key] = head
		}
	}
	if err := rows.Err(); err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	if err := rows.Close(); err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	result := SealedFailedArtifactSnapshot{Snapshot: ArtifactCommitSnapshot{
		StreamUID: current.StreamUID, BranchID: current.BranchID, BranchGeneration: current.BranchGeneration,
		ThroughAttempt: input.FailedAttempt, References: orderedSealedArtifactHeads(all),
	}, InputReferences: orderedSealedArtifactHeads(owningInput)}
	if err := tx.Commit(); err != nil {
		return SealedFailedArtifactSnapshot{}, false, schemaError(err)
	}
	return result, true, nil
}

type sealedArtifactHead struct {
	reference     ArtifactReference
	branchOrdinal int64
}

func orderedSealedArtifactHeads(heads map[string]sealedArtifactHead) []ArtifactReference {
	ordered := make([]sealedArtifactHead, 0, len(heads))
	for _, head := range heads {
		ordered = append(ordered, head)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.branchOrdinal != b.branchOrdinal {
			return a.branchOrdinal < b.branchOrdinal
		}
		if a.reference.Ordinal != b.reference.Ordinal {
			return a.reference.Ordinal < b.reference.Ordinal
		}
		return a.reference.VersionID < b.reference.VersionID
	})
	result := make([]ArtifactReference, 0, len(ordered))
	for _, head := range ordered {
		result = append(result, head.reference)
	}
	return result
}

func sealedFailedArtifactBoundary(ctx context.Context, tx *sql.Tx, input SealedFailedArtifactInput) (int64, int64, bool, error) {
	var status, receiptStatus, terminalType, correctionType string
	var inputRevision, finishedID, receiptID, terminalAttempt, correctionAttempt, terminalPublication, correctionPublication int64
	var terminalJSON, correctionJSON []byte
	err := tx.QueryRowContext(ctx, `
		SELECT attempt.status,attempt.claimed_input_revision,attempt.finished_event_id,receipt.status,receipt.event_id,
			terminal.event_type,terminal.publication_seq,terminal.runner_attempt,terminal.payload_json,
			correction.event_type,correction.publication_seq,correction.runner_attempt,correction.payload_json
		FROM transcript_runner_attempts attempt
		JOIN transcript_runner_receipts receipt ON receipt.stream_uid=attempt.stream_uid AND receipt.attempt=attempt.attempt
		JOIN transcript_events terminal ON terminal.stream_uid=attempt.stream_uid AND terminal.event_id=attempt.finished_event_id
		JOIN transcript_branch_events tm ON tm.stream_uid=terminal.stream_uid AND tm.event_id=terminal.event_id AND tm.branch_id=?
		JOIN transcript_events correction ON correction.stream_uid=attempt.stream_uid AND correction.event_id=?
		JOIN transcript_branch_events cm ON cm.stream_uid=correction.stream_uid AND cm.event_id=correction.event_id AND cm.branch_id=?
		WHERE attempt.stream_uid=? AND attempt.attempt=? AND terminal.source='payload' AND correction.source='payload'`,
		input.Projection.BranchID, input.CorrectionEventID, input.Projection.BranchID, input.Projection.StreamUID, input.FailedAttempt).
		Scan(&status, &inputRevision, &finishedID, &receiptStatus, &receiptID, &terminalType, &terminalPublication,
			&terminalAttempt, &terminalJSON, &correctionType, &correctionPublication, &correctionAttempt, &correctionJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, schemaError(err)
	}
	if status != "failed" || receiptStatus != "failed" || inputRevision != input.InputRevision ||
		finishedID != input.TerminalEventID || receiptID != finishedID || terminalType != "runner_finished" ||
		correctionType != "runner_checkpoint" || terminalAttempt != input.FailedAttempt || correctionAttempt != input.FailedAttempt ||
		correctionPublication >= terminalPublication || terminalPublication > input.Projection.ThroughPublicationSequence {
		return 0, 0, false, nil
	}
	var terminal, correction map[string]any
	if json.Unmarshal(terminalJSON, &terminal) != nil || terminal["status"] != "failed" || json.Unmarshal(correctionJSON, &correction) != nil {
		return 0, 0, false, ErrEventConflict
	}
	// Historical namespace envelopes are verified by the canonical loader but
	// cannot acquire this current-format selection proof implicitly.
	if input.CorrectionCondition.historicalSchema != "" {
		return 0, 0, false, nil
	}
	cause, present, err := ParseRunnerInterruptionCause(correction)
	if err != nil || !present {
		return 0, 0, false, ErrEventConflict
	}
	if cause.Condition != nil && cause.Condition.ContentID() == input.CorrectionCondition.ContentID() {
		return terminalPublication, correctionPublication, true, nil
	}
	if cause.ConditionRef != nil && cause.ConditionRef.SHA256 == input.CorrectionCondition.ContentID() &&
		cause.ConditionRef.Fingerprint == input.CorrectionCondition.Fingerprint() {
		return terminalPublication, correctionPublication, true, nil
	}
	return 0, 0, false, ErrEventConflict
}
