package transcript

import (
	"context"
	"encoding/json"
)

// Resolve audit ownership from immutable clone genesis, not the current parent
// head. Attempts retain their identity when copied; new child attempts lie after
// the copied event prefix. UNION makes even corrupt ancestry cycles finite.
func (r *Repository) roundUsageAuditSessions(ctx context.Context, streamUID, ownerID string, attempts []int64) (map[int64]string, error) {
	raw, err := json.Marshal(attempts)
	if err != nil {
		return nil, err
	}
	rows, err := r.readDatabase().QueryContext(ctx, `
 WITH RECURSIVE origins(stream_uid,owner_id,project_id,session_id,epoch,attempt,finished_event_id,claimed_at,finished_at) AS (
  SELECT stream.stream_uid,stream.owner_id,stream.project_id,stream.session_id,stream.epoch,
   runner.attempt,runner.finished_event_id,runner.claimed_at,runner.finished_at
  FROM transcript_streams stream
  JOIN transcript_runner_attempts runner ON runner.stream_uid=stream.stream_uid
  JOIN json_each(?) requested ON requested.value=runner.attempt
  WHERE stream.stream_uid=? AND stream.owner_id=?
  UNION
  SELECT parent.stream_uid,parent.owner_id,parent.project_id,parent.session_id,parent.epoch,
   runner.attempt,runner.finished_event_id,runner.claimed_at,runner.finished_at
  FROM origins child
  JOIN transcript_payload_genesis_receipts receipt ON receipt.stream_uid=child.stream_uid
   AND receipt.epoch=child.epoch AND receipt.owner_id=child.owner_id
   AND receipt.source_kind='canonical_clone' AND receipt.status='active'
   AND child.finished_event_id<=receipt.source_event_count
  JOIN transcript_streams parent ON parent.stream_uid=receipt.source_stream_uid
   AND parent.epoch=receipt.source_epoch AND parent.owner_id=child.owner_id AND parent.project_id=child.project_id
  JOIN transcript_runner_attempts runner ON runner.stream_uid=parent.stream_uid AND runner.attempt=child.attempt
   AND runner.claimed_at=child.claimed_at AND runner.finished_at=child.finished_at
 )
 SELECT origin.attempt,origin.session_id FROM origins origin
 WHERE NOT EXISTS (
  SELECT 1 FROM transcript_payload_genesis_receipts receipt WHERE receipt.stream_uid=origin.stream_uid
   AND receipt.epoch=origin.epoch AND receipt.owner_id=origin.owner_id
   AND receipt.source_kind='canonical_clone' AND receipt.status='active'
   AND origin.finished_event_id<=receipt.source_event_count
 )`, string(raw), streamUID, ownerID)
	if err != nil {
		return nil, schemaError(err)
	}
	defer rows.Close()
	result := map[int64]string{}
	for rows.Next() {
		var attempt int64
		var session string
		if err := rows.Scan(&attempt, &session); err != nil {
			return nil, schemaError(err)
		}
		if prior, exists := result[attempt]; exists && prior != session {
			return nil, ErrEventConflict
		}
		result[attempt] = session
	}
	return result, schemaError(rows.Err())
}
