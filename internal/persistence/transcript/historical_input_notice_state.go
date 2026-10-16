package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// HistoricalInputNoticeEventType is host presentation, not an input, model
// answer, execution checkpoint or authorization to replay uncertain work.
const HistoricalInputNoticeEventType = "host_recovery_notice"
const HistoricalInputNoticeKind = "historical_input_order_unknown"

type historicalInputNoticeState struct {
	Revision         int    `json:"revision"`
	Kind             string `json:"kind"`
	Owner            string `json:"owner"`
	Stream           string `json:"stream"`
	Epoch            int64  `json:"epoch"`
	Branch           string `json:"branch"`
	BranchGeneration int64  `json:"branch_generation"`
	Input            int64  `json:"input_revision"`
	Consumed         int64  `json:"consumed_revision"`
	Attempt          int64  `json:"attempt"`
	Claimed          int64  `json:"claimed_revision"`
	Finished         int64  `json:"finished_event"`
	UserEvent        int64  `json:"user_event"`
	UserSHA256       string `json:"user_sha256"`
	Destination      string `json:"destination"`
	RouteGeneration  int64  `json:"route_generation"`
}

func (state historicalInputNoticeState) record() ([]byte, string, error) {
	payload, err := json.Marshal(state)
	digest := sha256.Sum256(payload)
	return payload, "host-recovery-notice:" + hex.EncodeToString(digest[:]), err
}

func (r *Repository) historicalInputNoticeStateConn(ctx context.Context, conn *sql.Conn, stream Stream, destination string) (historicalInputNoticeState, string, bool, error) {
	state := historicalInputNoticeState{Revision: 1, Kind: HistoricalInputNoticeKind, Owner: stream.OwnerID,
		Stream: stream.UID, Epoch: stream.Epoch, Input: stream.InputRevision, Consumed: stream.ConsumedInputRevision, Destination: destination}
	if stream.Kind != StreamKindStandalone || stream.FrameID != "" || stream.SessionID != destination || destination == "" {
		return state, "", false, nil
	}
	var sessions int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_streams WHERE owner_id=? AND session_id=?`, stream.OwnerID, destination).Scan(&sessions); err != nil {
		return state, "", false, err
	}
	if sessions != 1 {
		return state, "", false, ErrEventConflict
	}
	evidence, err := r.freshMessageEvidenceConn(ctx, conn, stream)
	if err != nil || evidence.Authorized || evidence.Coverage.Complete || !evidence.Coverage.Pending() ||
		evidence.Coverage.HasControl || evidence.Coverage.HasRecovery {
		return state, "", false, err
	}
	var status string
	var finished sql.NullInt64
	err = conn.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision,status,finished_event_id FROM transcript_runner_attempts
		WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID).Scan(&state.Attempt, &state.Claimed, &status, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return state, "", false, nil
	}
	if err != nil || status != "completed" || !finished.Valid || stream.ConsumedInputRevision > state.Claimed {
		return state, "", false, err
	}
	var fenced bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts WHERE stream_uid=? AND status='running' AND expires_at>?)
		OR EXISTS(SELECT 1 FROM transcript_events WHERE stream_uid=? AND event_type='user_message_staged')`, stream.UID, r.now().UTC(), stream.UID).Scan(&fenced); err != nil {
		return state, "", false, err
	}
	if fenced {
		return state, "", false, nil
	}
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`, stream.UID).
		Scan(&state.Branch, &state.BranchGeneration); err != nil {
		return state, "", false, err
	}
	var receiptStatus, eventType, source string
	var attempt sql.NullInt64
	var frame sql.NullString
	var terminalPayload []byte
	var member bool
	err = conn.QueryRowContext(ctx, `SELECT receipt.event_id,receipt.status,e.event_type,e.source,e.runner_attempt,e.frame_event_id,e.payload_json,
		EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=?)
		FROM transcript_runner_receipts receipt JOIN transcript_events e ON e.stream_uid=receipt.stream_uid AND e.event_id=receipt.event_id
		WHERE receipt.stream_uid=? AND receipt.attempt=?`, state.Branch, stream.UID, state.Attempt).
		Scan(&state.Finished, &receiptStatus, &eventType, &source, &attempt, &frame, &terminalPayload, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return state, "", false, nil
	}
	if err != nil {
		return state, "", false, err
	}
	if state.Finished != finished.Int64 || receiptStatus != status || eventType != "runner_finished" || source != string(EventSourcePayload) ||
		!attempt.Valid || attempt.Int64 != state.Attempt || frame.Valid || !json.Valid(terminalPayload) {
		return state, "", false, ErrEventConflict
	}
	if !member {
		return state, "", false, nil
	}
	state.RouteGeneration, err = activeDeliveryGenerationConn(ctx, conn, stream.UID, destination)
	if err != nil {
		return state, "", false, err
	}
	text, err := historicalNoticeUserSourceConn(ctx, conn, &state)
	return state, text, state.UserEvent > 0, err
}

func historicalNoticeUserSourceConn(ctx context.Context, conn *sql.Conn, state *historicalInputNoticeState) (string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT e.event_id,e.event_type,e.client_message_id,e.source,e.runner_attempt,e.frame_event_id,e.payload_json
		FROM transcript_events e JOIN transcript_branch_events m ON m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=?
		WHERE e.stream_uid=? AND e.event_type IN ('user_message','user_input_response') ORDER BY e.event_id DESC LIMIT 1`, state.Branch, state.Stream)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind, client, source string
		var attempt sql.NullInt64
		var frame sql.NullString
		var payload []byte
		if err := rows.Scan(&id, &kind, &client, &source, &attempt, &frame, &payload); err != nil {
			return "", err
		}
		if source != string(EventSourcePayload) || attempt.Valid || frame.Valid || !json.Valid(payload) {
			return "", ErrEventConflict
		}
		inputKind, err := admittedEventBytesKind(kind, client, payload)
		if err != nil {
			return "", err
		}
		if inputKind != "message" {
			// A newer typed control cannot borrow an older ordinary input. This
			// is notice eligibility only, never reconstruction of lost admission.
			return "", nil
		}
		// Language is optional presentation metadata. The source/kind proof
		// above stays strict; unreadable text merely uses the English default.
		var content map[string]json.RawMessage
		_ = json.Unmarshal(payload, &content)
		var text string
		_ = json.Unmarshal(content["text"], &text)
		digest := sha256.Sum256(payload)
		state.UserEvent, state.UserSHA256 = id, hex.EncodeToString(digest[:])
		return text, nil
	}
	return "", rows.Err()
}
