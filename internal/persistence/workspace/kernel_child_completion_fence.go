package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

type KernelChildCompletionFence struct {
	Target DelegatedStopTarget
	Claim  transcriptstore.RunnerClaim
}

// The compatibility child summary is a projection, not a second terminal
// authority. Its final transaction cannot overwrite a newer input/incarnation
// or re-publish a cancellation already committed by the canonical Stop core.
func validateKernelChildCompletionTargetTx(ctx context.Context, tx workspaceTransaction, child KernelSupervisedChild, requestedStatus string, fence KernelChildCompletionFence) error {
	target := fence.Target
	scope, _, err := delegatedScopeTx(ctx, tx, child.OwnerUserID, child.ParentFrameID)
	if err != nil {
		return err
	}
	if scope.RootFrameID != child.RootFrameID {
		return transcriptstore.ErrClaimStale
	}
	actual, _, err := delegatedStopTargetWithPendingTx(ctx, tx, child.OwnerUserID, scope, child.FrameID, true)
	if err != nil {
		return err
	}
	if actual.FrameID != target.FrameID || actual.FrameIncarnationID != target.FrameIncarnationID || actual.StreamUID != target.StreamUID ||
		actual.StreamEpoch != target.StreamEpoch || actual.InputRevision != target.InputRevision || actual.InputMessageID != target.InputMessageID {
		return transcriptstore.ErrClaimStale
	}
	var attempt, claimed int64
	var status, runner, phase string
	var token []byte
	err = tx.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision,status,runner_id,phase,claim_token_sha256 FROM transcript_runner_attempts
		WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, target.StreamUID).Scan(&attempt, &claimed, &status, &runner, &phase, &token)
	if errors.Is(err, sql.ErrNoRows) && target.RunnerAttempt == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if target.RunnerAttempt > 0 && (attempt != target.RunnerAttempt || claimed != target.InputRevision) {
		return transcriptstore.ErrClaimStale
	}
	if claimed == target.InputRevision && (status == "cancelled" || target.RunnerAttempt == 0 && runner != "cancel-before-claim") {
		return transcriptstore.ErrClaimStale
	}
	if target.RunnerAttempt > 0 {
		claim := fence.Claim
		digest := sha256.Sum256([]byte(strings.TrimSpace(claim.ClaimToken)))
		if claim.OwnerID != child.OwnerUserID || claim.StreamUID != target.StreamUID || claim.Attempt != attempt ||
			claim.ClaimedInputRevision != claimed || claim.RunnerID != runner || claim.ClaimToken == "" || !bytes.Equal(digest[:], token) {
			return transcriptstore.ErrClaimStale
		}
		if requestedStatus == "awaiting_user_response" {
			if status != "running" || phase != "waiting_user" {
				return transcriptstore.ErrClaimStale
			}
		} else if status != requestedStatus {
			return transcriptstore.ErrClaimStale
		}
	}
	return nil
}
