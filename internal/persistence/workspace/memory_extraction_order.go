package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// A newer selected completion fences older delta-mode writes even when their
// physical cursor is equal. It also distinguishes legitimate truncation from
// an older source's overshoot. The original receipt index and completed-source
// reader remain the ordering authority.
const newerExtractionReceiptQuery = `SELECT receipt.source_key,receipt.source_json,receipt.source_sha256
	FROM memory_extraction_receipts receipt
	JOIN transcript_branch_events member ON member.stream_uid=? AND member.branch_id=?
	 AND member.event_id=json_extract(receipt.source_json,'$.Transcript.EventID')
	WHERE receipt.frame_id=? AND receipt.owner_user_id=? AND receipt.frame_incarnation_id=?
	 AND json_extract(receipt.source_json,'$.Transcript.StreamUID')=?
	 AND json_extract(receipt.source_json,'$.Transcript.StreamEpoch')=?
	 AND json_extract(receipt.source_json,'$.Transcript.PublicationSeq')>?
	ORDER BY json_extract(receipt.source_json,'$.Transcript.PublicationSeq') DESC LIMIT 1`

func fenceExtractionSourceOrderTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, identity extractionSourceIdentity, legacyCheck LegacyExtractionOrderCheck) error {
	proof := identity.source.Transcript
	if proof == nil {
		return fenceLegacyExtractionSourceOrderTx(ctx, tx, identity, legacyCheck)
	}
	var selected extractionStoredSource
	err := tx.QueryRowContext(ctx, newerExtractionReceiptQuery, proof.StreamUID, proof.BranchID,
		identity.source.FrameID, identity.source.OwnerUserID, identity.incarnation,
		proof.StreamUID, proof.StreamEpoch, proof.PublicationSeq).Scan(&selected.key, &selected.raw, &selected.digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read newer completed memory extraction: %w", err)
	}
	prior, err := identifyStoredExtractionSource(identity, selected)
	if err != nil {
		return err
	}
	if _, found, err := readExtractionReceiptTx(ctx, tx, prior, extractionReceiptStrict); err != nil {
		return err
	} else if !found {
		return ErrExtractionPlanConflict
	}
	completed := *prior.source.Transcript
	actual, err := tx.ReadCompletedMemorySource(ctx, completed.StreamUID, completed.OwnerID, completed.EventID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, transcriptstore.ErrEventConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("validate newer completed memory extraction: %w", err)
	}
	// Generation/branch may have changed since this receipt was committed, but
	// membership in the selected history and the immutable completion must still
	// match. Switching away and back must not erase the cursor's newer coverage.
	completed.BranchID, completed.BranchGeneration = actual.BranchID, actual.BranchGeneration
	if actual == completed && actual.StreamUID == proof.StreamUID && actual.StreamEpoch == proof.StreamEpoch &&
		actual.PublicationSeq > proof.PublicationSeq {
		return fmt.Errorf("%w: newer selected completion already advanced extraction", ErrExtractionSourceChanged)
	}
	return nil
}
