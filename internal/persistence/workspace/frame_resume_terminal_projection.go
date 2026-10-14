package workspace

import (
	"context"
	"database/sql"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

// projectFrameResumeTerminalTx returns true only for the existing legacy path.
// Transcript frames only settle their own dispatch against a verified receipt.
// They never repeat the original finish's Frame or presentation projection.
func projectFrameResumeTerminalTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	input CompleteCompatibilityFrameResumeDispatchInput, dispatch CompatibilityFrameResumeDispatch, recorded bool,
) (bool, error) {
	var streamUID, ownerID string
	err := tx.QueryRowContext(ctx, `SELECT stream.stream_uid,stream.owner_id
		FROM transcript_frame_authority authority
		JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid
			AND stream.owner_id=authority.owner_id AND stream.session_id=authority.session_id
			AND stream.epoch=authority.active_epoch
		WHERE stream.frame_id=? AND stream.kind='frame_ref'`, dispatch.FrameID).
		Scan(&streamUID, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		if input.RunnerTerminal != nil {
			return false, transcriptstore.ErrClaimStale
		}
		var transcriptFrame bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM transcript_streams
			WHERE frame_id=? AND kind='frame_ref')`, dispatch.FrameID).Scan(&transcriptFrame); err != nil {
			return false, err
		}
		if transcriptFrame {
			return false, transcriptstore.ErrEventConflict
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if input.RunnerTerminal == nil {
		if recorded {
			// This dispatch is already terminal. Its matching retry only reads
			// the immutable audit, including after a normal cancellation.
			return false, nil
		}
		// Terminal-only reconciliation owns no runner receipt. It may close
		// its dispatch only while the Frame still has the same terminal state;
		// it never rewrites status or terminal presentation.
		if dispatch.FrameStatus != input.Status || !isFrameResumeTerminalStatusForProjection(dispatch.FrameStatus) {
			return false, transcriptstore.ErrClaimStale
		}
		return false, nil
	}
	reference := *input.RunnerTerminal
	if reference.Claim.StreamUID != streamUID || reference.Claim.OwnerID != ownerID ||
		reference.Claim.RunnerID != "frame-resume:"+input.ResumeEventID {
		return false, transcriptstore.ErrClaimStale
	}
	return false, tx.ValidateRecordedRunnerTerminal(ctx, reference, input.Status)
}

func isFrameResumeTerminalStatusForProjection(status string) bool {
	return isCompatibilityResumeDispatchTerminal(status) || status == "canceled"
}
