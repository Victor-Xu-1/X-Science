package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// This is a view over the canonical event/admission, never another accepted
// input ledger. Legacy identifies a real event lacking original-body proof;
// only its existing complete-event equality path may replay it.
type FrameMessageIntentSnapshot struct {
	StreamUID        string
	EventID          string
	PayloadJSON      []byte
	InputRevision    int64
	BranchID         string
	BranchGeneration int64
	Legacy           bool
}

func readAcceptedFrameMessageIntentEvent(ctx context.Context, db MessageIntentQuery, intent FrameMessageIntent) (FrameMessageIntentSnapshot, bool, error) {
	var result FrameMessageIntentSnapshot
	err := db.QueryRowContext(ctx, `SELECT active_stream_uid FROM transcript_frame_authority
		WHERE owner_id=? AND session_id=? AND read_authority='transcript_payload_v1' AND write_authority='transcript_payload_v1'`, intent.OwnerID, intent.FrameID).Scan(&result.StreamUID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, ErrMessageIntentUnavailable
	}
	if err != nil {
		return result, false, err
	}
	for _, id := range messageIntentCandidateIDs(intent) {
		var raw, source, kind string
		err := db.QueryRowContext(ctx, `SELECT event_id,COALESCE(payload_json,''),source,event_type
			FROM transcript_events WHERE stream_uid=? AND client_message_id=?`, result.StreamUID, id).Scan(&result.EventID, &raw, &source, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return result, false, err
		}
		var payload struct {
			Intent *FrameMessageIntent `json:"messageIntent"`
		}
		if kind != "user_message" && kind != "user_input_response" {
			return result, true, ErrMessageIntentUnavailable
		}
		if source != string(EventSourcePayload) {
			result.Legacy = source == string(EventSourceFrameRef)
			return result, true, ErrMessageIntentUnavailable
		}
		if json.Unmarshal([]byte(raw), &payload) != nil {
			return result, true, ErrMessageIntentUnavailable
		}
		if payload.Intent == nil {
			result.Legacy = true
			return result, true, ErrMessageIntentUnavailable
		}
		if !payload.Intent.Valid() {
			return result, true, ErrMessageIntentUnavailable
		}
		if *payload.Intent != intent || id != intent.InputMessageID {
			return result, true, ErrMessageIntentConflict
		}
		result.PayloadJSON = []byte(raw)
		return result, true, nil
	}
	return result, false, nil
}

// The caller supplies one transaction/read snapshot for scope, cancellation,
// canonical acceptance and queue arbitration.
func ReadAcceptedFrameMessageSnapshot(ctx context.Context, db MessageIntentQuery, intent FrameMessageIntent) (FrameMessageIntentSnapshot, bool, error) {
	result, found, err := readAcceptedFrameMessageIntentEvent(ctx, db, intent)
	if err != nil || !found {
		return result, found, err
	}
	err = db.QueryRowContext(ctx, `SELECT input_revision,branch_id,branch_generation FROM transcript_input_admissions
		WHERE stream_uid=? AND event_id=? AND source_kind='event'`, result.StreamUID, result.EventID).Scan(
		&result.InputRevision, &result.BranchID, &result.BranchGeneration)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (result.InputRevision <= 0 || result.BranchID == "" || result.BranchGeneration <= 0) {
		return result, true, ErrMessageIntentUnavailable
	}
	return result, true, err
}
