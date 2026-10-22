package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// A larger cursor can mean either a legitimate history truncation or a newer
// completed extraction. Only the latter forbids this older source's overshoot
// repair. The existing receipt index bounds the candidate query; the shared
// Transcript completed-source reader remains the history authority.
const newerExtractionReceiptQuery = `SELECT receipt.source_json,receipt.source_sha256
	FROM memory_extraction_receipts receipt
	JOIN transcript_branch_events member ON member.stream_uid=? AND member.branch_id=?
	 AND member.event_id=json_extract(receipt.source_json,'$.Transcript.EventID')
	WHERE receipt.frame_id=? AND receipt.owner_user_id=? AND receipt.frame_incarnation_id=?
	 AND json_extract(receipt.source_json,'$.Transcript.StreamUID')=?
	 AND json_extract(receipt.source_json,'$.Transcript.StreamEpoch')=?
	 AND json_extract(receipt.source_json,'$.Transcript.PublicationSeq')>?
	ORDER BY json_extract(receipt.source_json,'$.Transcript.PublicationSeq') DESC LIMIT 1`

func fenceExtractionOvershootTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, identity extractionSourceIdentity) error {
	proof := identity.source.Transcript
	if proof == nil {
		// The server owns the legacy strict journal guard and checks its actual
		// completed prefix while this same cursor CAS remains fenced by SQLite.
		return nil
	}
	var raw, digest string
	err := tx.QueryRowContext(ctx, newerExtractionReceiptQuery, proof.StreamUID, proof.BranchID,
		identity.source.FrameID, identity.source.OwnerUserID, identity.incarnation,
		proof.StreamUID, proof.StreamEpoch, proof.PublicationSeq).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read newer completed memory extraction: %w", err)
	}
	var candidate CompletedExtractionSource
	if extractionDigest([]byte(raw)) != digest || json.Unmarshal([]byte(raw), &candidate) != nil {
		return ErrExtractionPlanConflict
	}
	prior, err := identifyExtractionSource(candidate)
	if err != nil || prior.source.Transcript == nil || prior.source.OwnerUserID != identity.source.OwnerUserID ||
		prior.source.ProjectID != identity.source.ProjectID || prior.source.FrameID != identity.source.FrameID || prior.incarnation != identity.incarnation {
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
