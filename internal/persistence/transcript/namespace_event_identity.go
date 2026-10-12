package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

func NamespaceHistoricalEventDigest(event Event) (string, error) {
	if event.Source != EventSourcePayload || event.FrameEventID != nil || event.StreamUID == "" || event.EventID <= 0 ||
		event.PublicationSeq <= 0 || event.ClientMessageID == "" || len(event.PayloadJSON) > MaxEventPayloadBytes || !json.Valid(event.PayloadJSON) {
		return "", errors.New("native namespace event source is invalid")
	}
	switch event.Type {
	case "runner_checkpoint", "runner_finished", AskUserPromptEventType, AskUserResultEventType:
	default:
		return "", errors.New("event is not a host-owned namespace metadata source")
	}
	event.CreatedAt = event.CreatedAt.UTC()
	encoded, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("x-science.namespace-original-event.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}

// IsHistoricalNamespaceEvent verifies this repository's own original bytes.
// A separate Transcript database without a cutoff inventory is not treated as
// covered by another file's migration, nor can user/upload events gain proof.
func (r *Repository) IsHistoricalNamespaceEvent(ctx context.Context, ownerID string, event Event) (bool, error) {
	if r == nil || r.db == nil || ownerID == "" {
		return false, errors.New("native namespace event owner is required")
	}
	return isHistoricalNamespaceEventQuery(ctx, r.readDatabase(), ownerID, event)
}

func isHistoricalNamespaceEventQuery(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ownerID string, event Event) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if event.Source != EventSourcePayload || event.FrameEventID != nil {
		return false, nil
	}
	switch event.Type {
	case "runner_checkpoint", "runner_finished", AskUserPromptEventType, AskUserResultEventType:
	default:
		return false, nil
	}
	digest, err := NamespaceHistoricalEventDigest(event)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='namespace_transcript_event_receipts')`).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	var original string
	err = query.QueryRowContext(ctx, `SELECT proof.source_sha256 FROM namespace_transcript_event_receipts proof
		JOIN transcript_streams stream ON stream.stream_uid=proof.stream_uid
		JOIN transcript_events event ON event.stream_uid=proof.stream_uid AND event.event_id=proof.event_id
		WHERE proof.stream_uid=? AND proof.event_id=? AND stream.owner_id=?
			AND event.event_type=? AND event.source='payload' AND event.client_message_id=? AND event.publication_seq=? AND event.payload_json=?`,
		event.StreamUID, event.EventID, ownerID, event.Type, event.ClientMessageID, event.PublicationSeq, event.PayloadJSON).Scan(&original)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if original != digest {
		return false, errors.New("historical namespace event source identity mismatch")
	}
	return true, nil
}
