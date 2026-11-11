package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// ListProjectedUserSources resolves only the requested single-event user rows.
// Source type, ownership and branch membership come from the transcript, never
// from a materialized message's role or presentation fields.
// The caller bounds the request to its authorized Web page; event scan batch
// limits do not restrict the number of messages in that page.
func (r *Repository) ListProjectedUserSources(ctx context.Context, owner string, expected ProjectionSnapshot, sequences []int64) ([]ProjectedEvent, error) {
	db := r.readDatabase()
	if db == nil {
		return nil, ErrSchemaUnavailable
	}
	if strings.TrimSpace(owner) == "" || expected.StreamUID == "" || !validTranscriptBranchID(expected.BranchID) || expected.BranchGeneration <= 0 || expected.ThroughPublicationSequence <= 0 {
		return nil, ErrBranchStateStale
	}
	if len(sequences) == 0 {
		return nil, nil
	}
	for _, sequence := range sequences {
		if sequence <= 0 || sequence > expected.ThroughPublicationSequence {
			return nil, ErrBranchStateStale
		}
	}
	requested, err := json.Marshal(sequences)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	kind, frameID, actual, err := projectionSnapshotForBranchTx(ctx, tx, expected.StreamUID, owner, expected.BranchID)
	if err != nil {
		return nil, schemaError(err)
	}
	if kind != string(StreamKindFrameRef) || actual.BranchGeneration != expected.BranchGeneration || expected.ThroughPublicationSequence > actual.ThroughPublicationSequence {
		return nil, ErrBranchStateStale
	}
	rows, err := tx.QueryContext(ctx, `WITH requested(sequence) AS (
		SELECT value FROM json_each(?) WHERE type='integer'
	)
		SELECT event.stream_uid,event.event_id,event.publication_seq,event.client_message_id,event.event_type,event.source,
			event.runner_attempt,event.payload_json,event.frame_event_id,event.created_at,
			frame.frame_id,frame.event_type,frame.payload
		FROM requested
		JOIN transcript_events event ON event.stream_uid=? AND event.publication_seq=requested.sequence
		JOIN transcript_branch_events membership ON membership.stream_uid=event.stream_uid AND membership.event_id=event.event_id AND membership.branch_id=?
		LEFT JOIN frame_events frame ON event.source='frame_ref' AND frame.id=event.frame_event_id
		WHERE event.event_type IN ('user_message','history_user_message')
			AND event.source IN ('payload','frame_ref') AND event.runner_attempt IS NULL
		ORDER BY event.publication_seq`, string(requested), expected.StreamUID, expected.BranchID)
	if err != nil {
		return nil, schemaError(err)
	}
	var sources []ProjectedEvent
	for rows.Next() {
		event, payload, err := scanProjectedEvent(rows, StreamKind(kind), frameID)
		if err != nil {
			_ = rows.Close()
			return nil, schemaError(err)
		}
		var content map[string]any
		if json.Unmarshal(payload, &content) != nil {
			_ = rows.Close()
			return nil, ErrEventConflict
		}
		if HiddenConversationControlIntent(content) != "" || IsRunnerInternalFact(event, payload) {
			continue
		}
		sources = append(sources, ProjectedEvent{Event: event, ResolvedPayloadJSON: payload})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, schemaError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, schemaError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, schemaError(err)
	}
	return sources, nil
}
