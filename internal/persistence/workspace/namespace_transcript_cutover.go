package workspace

import (
	"context"
	"database/sql"

	transcriptstore "x-science/internal/persistence/transcript"
)

func captureNamespaceTranscriptReceipts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT stream_uid,event_id,publication_seq,client_message_id,event_type,runner_attempt,payload_json,created_at
		FROM transcript_events WHERE source='payload' AND event_type IN ('runner_checkpoint','runner_finished','ask_user_prompt','ask_user_result') ORDER BY stream_uid,event_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var event transcriptstore.Event
		var attempt sql.NullInt64
		event.Source = transcriptstore.EventSourcePayload
		if err := rows.Scan(&event.StreamUID, &event.EventID, &event.PublicationSeq, &event.ClientMessageID, &event.Type, &attempt, &event.PayloadJSON, &event.CreatedAt); err != nil {
			return err
		}
		if attempt.Valid {
			value := attempt.Int64
			event.RunnerAttempt = &value
		}
		digest, err := transcriptstore.NamespaceHistoricalEventDigest(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_transcript_event_receipts(stream_uid,event_id,source_sha256) VALUES(?,?,?)`, event.StreamUID, event.EventID, digest); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE TRIGGER namespace_transcript_event_receipts_insert_guard BEFORE INSERT ON namespace_transcript_event_receipts BEGIN SELECT RAISE(ABORT,'namespace transcript cutover inventory is sealed'); END`,
		`CREATE TRIGGER namespace_transcript_event_receipts_update_guard BEFORE UPDATE ON namespace_transcript_event_receipts BEGIN SELECT RAISE(ABORT,'namespace transcript cutover inventory is immutable'); END`,
		`CREATE TRIGGER namespace_transcript_event_receipts_delete_guard BEFORE DELETE ON namespace_transcript_event_receipts BEGIN SELECT RAISE(ABORT,'namespace transcript cutover inventory is immutable'); END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := transcriptstore.CaptureNamespaceHistoryAuditReceipts(ctx, tx); err != nil {
		return err
	}
	return captureNamespaceComputeReceipts(ctx, tx)
}
