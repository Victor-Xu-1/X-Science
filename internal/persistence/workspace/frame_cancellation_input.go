package workspace

import (
	"context"
	"database/sql"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

var (
	ErrFrameCancellationInputStale       = errors.New("frame cancellation input changed")
	ErrFrameCancellationInputUnavailable = errors.New("frame cancellation input authority is unavailable")
)

// Match before any Frame, runner, detached-kernel or remote cancellation fact
// is written. The caller holds the existing cancellation/admission transaction.
func requireFrameCancellationInputTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, ownerID, frameID, expected string) error {
	var queued bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM queued_user_messages WHERE frame_id=? AND state IN ('queued','delivering'))`, frameID).Scan(&queued); err != nil {
		return err
	}
	if queued {
		return ErrFrameCancellationInputStale
	}
	clientID, streamUID, err := readFrameCancellationInputTx(ctx, tx, ownerID, frameID)
	if err != nil {
		return err
	}
	if clientID != expected {
		return ErrFrameCancellationInputStale
	}
	var claimedRevision int64
	err = tx.QueryRowContext(ctx, `SELECT claimed_input_revision FROM transcript_runner_attempts WHERE stream_uid=? AND status='running' ORDER BY attempt DESC LIMIT 1`, streamUID).Scan(&claimedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var activeInput string
	err = tx.QueryRowContext(ctx, `SELECT event.client_message_id FROM transcript_input_admissions admission
		JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
		JOIN transcript_branch_state branch ON branch.stream_uid=admission.stream_uid
		JOIN transcript_branch_events membership ON membership.stream_uid=admission.stream_uid AND membership.branch_id=branch.active_branch_id AND membership.event_id=event.event_id
		WHERE admission.stream_uid=? AND admission.input_revision<=? AND admission.source_kind='event' AND admission.input_kind='message' AND admission.classification_version=1
		ORDER BY admission.input_revision DESC LIMIT 1`, streamUID, claimedRevision).Scan(&activeInput)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFrameCancellationInputUnavailable
	}
	if err != nil {
		return err
	}
	if activeInput != expected {
		return ErrFrameCancellationInputStale
	}
	return nil
}

func readFrameCancellationInputTx(ctx context.Context, tx workspaceTransaction, ownerID, frameID string) (string, string, error) {
	var clientID, streamUID string
	err := tx.QueryRowContext(ctx, `SELECT event.client_message_id,stream.stream_uid
		FROM transcript_frame_authority authority
		JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid AND stream.epoch=authority.active_epoch
		JOIN transcript_branch_state branch ON branch.stream_uid=stream.stream_uid
		JOIN transcript_input_admissions admission ON admission.stream_uid=stream.stream_uid
		JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
		JOIN transcript_branch_events membership ON membership.stream_uid=stream.stream_uid AND membership.branch_id=branch.active_branch_id AND membership.event_id=event.event_id
		WHERE authority.owner_id=? AND authority.session_id=? AND stream.owner_id=? AND stream.frame_id=?
		AND stream.kind='frame_ref' AND stream.root_frame_id=stream.frame_id
		AND authority.read_authority='transcript_payload_v1' AND authority.write_authority='transcript_payload_v1'
		AND admission.source_kind='event' AND admission.input_kind='message' AND admission.classification_version=1
		AND admission.input_revision<=stream.input_revision
		ORDER BY admission.input_revision DESC LIMIT 1`, ownerID, frameID, ownerID, frameID).Scan(&clientID, &streamUID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && clientID == "" {
		return "", "", ErrFrameCancellationInputUnavailable
	}
	return clientID, streamUID, err
}
