package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Every claim path, including checkpoint recovery and generic claim-next,
// rechecks the same cancellation tombstone. A reclaimed lease is not a new
// permission to revive a maintenance request that the user already cancelled.
func validateContextCompactionClaimAdmissionConn(ctx context.Context, conn *sql.Conn, stream Stream) error {
	installed, err := contextCompactionLedgerForStream(ctx, conn, stream.UID)
	if err != nil || !installed {
		return err
	}
	var raw []byte
	err = conn.QueryRowContext(ctx, `SELECT event.payload_json FROM transcript_input_admissions admission
  JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
  WHERE admission.stream_uid=? AND admission.input_revision=? AND admission.source_kind='event'
   AND admission.input_kind='message' AND event.source='payload'
   AND json_extract(event.payload_json,'$.inputData.turn_intent')='compact-context'`, stream.UID, stream.InputRevision).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ErrContextCompactionInvalid
	}
	request, err := ContextCompactionRequestFromPayload(payload)
	if err != nil {
		return err
	}
	if request == nil {
		return ErrContextCompactionInvalid
	}
	target, _, _, err := contextCompactionTargetConn(ctx, conn, stream.OwnerID, stream.FrameID)
	if err != nil {
		return err
	}
	if !sameContextCompactionIdentity(target, request.Expected) {
		return ErrContextCompactionStale
	}
	intent := request.MessageIntent(stream.OwnerID, stream.FrameID)
	if err := CheckFrameMessageIntentAdmission(ctx, conn, stream.FrameID, intent.InputMessageID, &intent); err != nil {
		return err
	}
	var settled bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts WHERE stream_uid=?
  AND claimed_input_revision=? AND status IN ('completed','failed','cancelled'))`, stream.UID, stream.InputRevision).Scan(&settled); err != nil {
		return err
	}
	if settled {
		return ErrMessageIntentCancelled
	}
	return nil
}
