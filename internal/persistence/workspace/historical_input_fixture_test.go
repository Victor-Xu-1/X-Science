package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Historical migration fixtures must not run schema77's admission writer on
// an older installed database. Reuse the original event staging path for exact
// bytes, IDs, branch membership and delivery intents, then construct only the
// old admission transition. This test-only seed is never a runtime fallback.
func appendHistoricalMigrationUserEvent(t *testing.T, ctx context.Context, db *sql.DB, repo *transcriptstore.Repository,
	input transcriptstore.AppendUserEventInput,
) (transcriptstore.Event, bool, error) {
	t.Helper()
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM workspace_schema_migrations`).Scan(&version); err != nil {
		return transcriptstore.Event{}, false, err
	}
	if version < 23 || version >= 77 {
		return transcriptstore.Event{}, false, fmt.Errorf("historical input fixture requires installed schema23..76, got %d", version)
	}
	staged, err := repo.StageUserEvent(ctx, input)
	if err != nil || staged.Admitted {
		return staged.Event, false, err
	}
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		update, err := tx.ExecContext(ctx, `UPDATE transcript_events SET event_type='user_message'
			WHERE stream_uid=? AND event_id=? AND event_type='user_message_staged' AND runner_attempt IS NULL`,
			input.StreamUID, staged.Event.EventID)
		if err != nil {
			return err
		}
		if rows, err := update.RowsAffected(); err != nil || rows != 1 {
			return fmt.Errorf("historical staged input was not unique: rows=%d err=%v", rows, err)
		}
		update, err = tx.ExecContext(ctx, `UPDATE transcript_streams SET input_revision=input_revision+1,updated_at=?
			WHERE stream_uid=? AND owner_id=?`, staged.Event.CreatedAt, input.StreamUID, input.OwnerID)
		if err != nil {
			return err
		}
		if rows, err := update.RowsAffected(); err != nil || rows != 1 {
			return fmt.Errorf("historical input owner was not unique: rows=%d err=%v", rows, err)
		}
		return nil
	})
	if err != nil {
		return transcriptstore.Event{}, false, err
	}
	event := staged.Event
	event.Type = "user_message"
	return event, staged.Created, nil
}

func appendHistoricalMigrationFrameUserEvent(t *testing.T, ctx context.Context, db *sql.DB, repo *transcriptstore.Repository,
	input transcriptstore.AppendFrameUserEventInput,
) (transcriptstore.Event, bool, error) {
	t.Helper()
	if input.FrameEventID == "" || input.MessageUUID == "" || input.Text == "" || input.MessageOrigin != "" ||
		len(input.InputData) != 0 || len(input.RuntimeConfig) != 0 || len(input.ArtifactReferences) != 0 {
		return transcriptstore.Event{}, false, fmt.Errorf("historical frame fixture requires its original initial-message identity")
	}
	var frameStatus string
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT f.status,s.input_revision FROM transcript_streams s
		JOIN frames f ON f.id=s.frame_id WHERE s.stream_uid=? AND s.owner_id=?`, input.StreamUID, input.OwnerID).Scan(&frameStatus, &revision); err != nil {
		return transcriptstore.Event{}, false, err
	}
	// Both were legal initial states for the historical writer. Preserve its
	// pending projector fixtures rather than rewriting their Frame status just
	// to use today's helper. The old activation did not change pending Frames.
	if (frameStatus != "processing" && frameStatus != "pending") || revision != 0 {
		return transcriptstore.Event{}, false, fmt.Errorf("historical frame fixture requires a pending or processing Frame with no initial input")
	}
	// These payload-owned epochs never stored the caller's legacy FrameEventID.
	// Preserve that input above and reproduce the former writer's exact bytes.
	payload, err := json.Marshal(map[string]any{
		"messageUuid": input.MessageUUID, "clientMessageId": input.ClientMessageID,
		"text": input.Text, "role": "user", "_uuid": input.MessageUUID, "messageOrigin": "task_intent",
		"content": []any{map[string]any{"type": "text", "text": input.Text}},
	})
	if err != nil {
		return transcriptstore.Event{}, false, err
	}
	return appendHistoricalMigrationUserEvent(t, ctx, db, repo, transcriptstore.AppendUserEventInput{
		StreamUID: input.StreamUID, OwnerID: input.OwnerID, ClientMessageID: input.ClientMessageID,
		PayloadJSON: payload, Destinations: input.Destinations,
	})
}
