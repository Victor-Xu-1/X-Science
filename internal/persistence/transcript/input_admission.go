package transcript

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AdmissionClassificationV1 is immutable policy: generic user_message is an
// ordinary input; an input_response is ordinary only with canonical composer
// provenance. Future policy changes require a new classification version,
// never reinterpretation of already recorded kinds.
const AdmissionClassificationV1 = 1

func admittedEventKind(ctx context.Context, query transcriptQueryRower, stream Stream, event Event) (string, error) {
	if event.Type == "user_message" {
		return "message", nil
	}
	if event.Type != "user_input_response" {
		return "", ErrEventConflict
	}
	payload := event.PayloadJSON
	if event.Source == EventSourceFrameRef {
		if event.FrameEventID == nil {
			return "", ErrEventConflict
		}
		if err := query.QueryRowContext(ctx, `SELECT payload FROM frame_events
			WHERE id=? AND frame_id=? AND event_type=?`, *event.FrameEventID, stream.FrameID, event.Type).Scan(&payload); err != nil {
			return "", err
		}
	}
	return admittedEventBytesKind(event.Type, event.ClientMessageID, payload)
}

func admittedEventBytesKind(eventType, clientID string, payload []byte) (string, error) {
	if eventType == "user_message" {
		return "message", nil
	}
	if eventType != "user_input_response" {
		return "", ErrEventConflict
	}
	if IsCanonicalFrameUserMessage(eventType, clientID, payload) {
		return "message", nil
	}
	return "control", nil
}

// admitEventInputConn is called only after the existing append/admit succeeds.
// Both the only counter and its source fact commit in the caller's transaction.
func admitEventInputConn(ctx context.Context, conn *sql.Conn, stream Stream, event Event, now time.Time) error {
	kind, err := admittedEventKind(ctx, conn, stream, event)
	if err != nil {
		return err
	}
	var revision int64
	if err := conn.QueryRowContext(ctx, `UPDATE transcript_streams
		SET input_revision=input_revision+1,updated_at=? WHERE stream_uid=? RETURNING input_revision`, now, stream.UID).Scan(&revision); err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx, `INSERT INTO transcript_input_admissions
		(stream_uid,input_revision,source_kind,input_kind,classification_version,event_id,branch_id,branch_generation)
		SELECT ?,?,'event',?,1,?,active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`,
		stream.UID, revision, kind, event.EventID, stream.UID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrEventConflict
	}
	return nil
}

// RecordKernelRecoveryAdmission binds a recovery revision to the immutable
// transition already created in this transaction. Workspace verifies that exact
// transition; the migration adds its corresponding storage guard. No kernel
// table becomes a transcript reader dependency or an alternate revision source.
func (tx *ImmediateTransaction) RecordKernelRecoveryAdmission(
	ctx context.Context, streamUID string, revision int64, operationID string, stateVersion int64,
) error {
	if tx == nil || tx.conn == nil || strings.TrimSpace(streamUID) == "" || revision <= 0 ||
		strings.TrimSpace(operationID) == "" || stateVersion <= 0 {
		return ErrEventConflict
	}
	result, err := tx.conn.ExecContext(ctx, `INSERT INTO transcript_input_admissions
		(stream_uid,input_revision,source_kind,input_kind,classification_version,operation_id,operation_state_version,branch_id,branch_generation)
		SELECT ?,?,'kernel_recovery','recovery',1,?,?,active_branch_id,generation
		FROM transcript_branch_state WHERE stream_uid=?`, streamUID, revision, operationID, stateVersion, streamUID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrEventConflict
	}
	tx.markInputAdded(true, nil)
	return nil
}
