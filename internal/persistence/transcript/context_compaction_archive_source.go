package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Boundary provenance authorizes only a derived archive read. It does not
// issue or replay an execution, and survives later active-branch changes.
func (r *Repository) ContextCompactionBoundary(ctx context.Context, owner, frame string, request ContextCompactionRequest) (Event, int64, error) {
	if r == nil || r.readDatabase() == nil {
		return Event{}, 0, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return Event{}, 0, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return Event{}, 0, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
	receipt, err := contextCompactionReceiptConn(ctx, conn, owner, frame, request, r.now().UTC())
	if err != nil {
		return Event{}, 0, err
	}
	if receipt.State != "committed" {
		return Event{}, 0, ErrContextCompactionConflict
	}
	event, found, err := findEventByClientID(ctx, conn, request.Expected.StreamUID, "context-compaction-boundary:"+request.RequestID)
	if err != nil {
		return Event{}, 0, err
	}
	if !found || event.EventID != receipt.BoundaryEventID {
		return Event{}, 0, ErrEventConflict
	}
	proof, err := ReadContextCompactionBoundaryProof(ctx, conn, owner, request.Expected.StreamUID, event.EventID)
	if err != nil {
		return Event{}, 0, err
	}
	if proof == nil {
		return Event{}, 0, ErrEventConflict
	}
	var member bool
	var generation int64
	err = conn.QueryRowContext(ctx, `SELECT generation,EXISTS(SELECT 1 FROM transcript_branch_events WHERE stream_uid=? AND branch_id=? AND event_id=?)
		FROM transcript_branch_state WHERE stream_uid=?`, request.Expected.StreamUID, request.Expected.BranchID, event.EventID, request.Expected.StreamUID).Scan(&generation, &member)
	if err != nil {
		return Event{}, 0, err
	}
	if !member {
		return Event{}, 0, sql.ErrNoRows
	}
	return event, generation, nil
}

// Existing archive reconciliation asks for only missing projections from the
// current owned stream. This bounded query is also cancellation/deadline bound.
func (r *Repository) PendingContextCompactionArchiveRequests(ctx context.Context, owner, frame string) ([]ContextCompactionRequest, error) {
	if r == nil || r.readDatabase() == nil {
		return nil, ErrSchemaUnavailable
	}
	rows, err := r.readDatabase().QueryContext(ctx, `SELECT event.payload_json FROM transcript_frame_authority authority
		JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid AND stream.owner_id=authority.owner_id AND stream.epoch=authority.active_epoch
		JOIN frames frame ON frame.id=stream.frame_id JOIN projects project ON project.id=frame.project_id AND project.user_id=stream.owner_id
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
		JOIN transcript_events event ON event.stream_uid=stream.stream_uid AND event.event_type='runner_checkpoint'
		LEFT JOIN compaction_archives archive ON archive.frame_id=frame.id AND archive.source_event_id=event.event_id
		WHERE authority.owner_id=? AND authority.session_id=? AND archive.id IS NULL
			AND json_extract(event.payload_json,'$.toolPhase')=? AND json_extract(event.payload_json,'$.compaction.schema')=?
			AND json_extract(event.payload_json,'$.maintenance.expected.frame_incarnation_id')=frame.incarnation_id
			AND json_extract(event.payload_json,'$.maintenance.expected.root_frame_incarnation_id')=root.incarnation_id
		ORDER BY event.publication_seq LIMIT 4`, owner, frame, ContextCompactionPhase, ContextCompactionBoundarySchema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ContextCompactionRequest{}
	for rows.Next() {
		var raw []byte
		var value struct {
			Request ContextCompactionRequest `json:"maintenance"`
		}
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &value) != nil || value.Request.Validate() != nil {
			return nil, ErrContextCompactionConflict
		}
		result = append(result, value.Request)
	}
	return result, rows.Err()
}
