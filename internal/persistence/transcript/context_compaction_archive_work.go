package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ContextCompactionArchiveWork is a navigation result, not archive authority.
// The existing boundary/source and archive transaction revalidate every fence.
type ContextCompactionArchiveWork struct {
	OwnerID, ProjectID, FrameID, StreamUID string
	BoundaryEventID                        int64
	Request                                ContextCompactionRequest
}

// Keep the indexed client-ID range before inspecting boundary JSON. CROSS JOIN
// keeps the event lookup relative to an owned active stream rather than scanning
// every transcript payload. The cyclic cursor cannot authorize a write.
const pendingContextCompactionArchiveSQL = `SELECT stream.owner_id,project.id,frame.id,event.stream_uid,event.event_id,event.payload_json
 FROM transcript_frame_authority authority
 JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid AND stream.owner_id=authority.owner_id AND stream.epoch=authority.active_epoch
 JOIN frames frame ON frame.id=stream.frame_id
 JOIN projects project ON project.id=frame.project_id AND project.user_id=stream.owner_id
 JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
 CROSS JOIN transcript_events event ON event.stream_uid=stream.stream_uid
  AND event.client_message_id>='context-compaction-boundary:' AND event.client_message_id<'context-compaction-boundary;'
 JOIN transcript_branch_events member ON member.stream_uid=event.stream_uid AND member.event_id=event.event_id
  AND member.branch_id=json_extract(event.payload_json,'$.maintenance.expected.branch_id')
 LEFT JOIN compaction_archives archive ON archive.frame_id=frame.id AND archive.source_event_id=event.event_id
 LEFT JOIN realtime_events notice ON notice.id='context-compaction-archive:'||json_extract(event.payload_json,'$.maintenance.request_id')||':'||event.event_id
  AND notice.user_id=stream.owner_id AND notice.project_id=project.id AND notice.frame_id=frame.id AND notice.event_type='compaction_status'
 WHERE authority.read_authority='transcript_payload_v1' AND authority.write_authority='transcript_payload_v1'
  AND ((? AND archive.id IS NULL) OR (NOT ? AND archive.id IS NOT NULL AND notice.id IS NULL))
  AND json_extract(event.payload_json,'$.maintenance.expected.frame_incarnation_id')=frame.incarnation_id
  AND json_extract(event.payload_json,'$.maintenance.expected.root_frame_id')=root.id
  AND json_extract(event.payload_json,'$.maintenance.expected.root_frame_incarnation_id')=root.incarnation_id
  AND (` + manualCompactionBoundarySQL + `)
 ORDER BY CASE WHEN event.stream_uid>? OR (event.stream_uid=? AND event.event_id>?) THEN 0 ELSE 1 END,
  event.stream_uid,event.event_id LIMIT 1`

// NextPendingContextCompactionArchive selects at most one proven committed
// boundary. A malformed selected request retains its coordinates with the error
// so a caller can advance its fairness cursor without granting it authority.
func (r *Repository) NextPendingContextCompactionArchive(ctx context.Context, missingArchive bool, afterStream string, afterEvent int64) (*ContextCompactionArchiveWork, error) {
	if r == nil || r.readDatabase() == nil {
		return nil, ErrSchemaUnavailable
	}
	var work ContextCompactionArchiveWork
	var raw []byte
	err := r.readDatabase().QueryRowContext(ctx, pendingContextCompactionArchiveSQL, missingArchive, missingArchive, afterStream, afterStream, afterEvent).
		Scan(&work.OwnerID, &work.ProjectID, &work.FrameID, &work.StreamUID, &work.BoundaryEventID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var payload struct {
		Request ContextCompactionRequest `json:"maintenance"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Request.Validate() != nil {
		return &work, ErrContextCompactionConflict
	}
	work.Request = payload.Request
	return &work, nil
}
