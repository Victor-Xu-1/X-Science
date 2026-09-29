package transcript

import (
	"context"
	"encoding/json"
)

// CompletedRoundArtifactReferences projects final generated versions for each
// completed input revision. Reclaims share the admitted input revision; a new
// user input does not. Only durable commits or previously validated, typed
// generation references establish ownership; visible text never does.
// The terminal branch boundary prevents later writes from changing old rounds.
func (r *Repository) CompletedRoundArtifactReferences(ctx context.Context, streamUID, ownerID string, attempts []int64) (map[int64][]ArtifactReference, error) {
	result := make(map[int64][]ArtifactReference, len(attempts))
	if len(attempts) == 0 {
		return result, nil
	}
	if len(attempts) > 2000 {
		return nil, ErrProjectionResourceLimit
	}
	if _, err := r.GetStream(ctx, streamUID, ownerID); err != nil {
		return nil, err
	}
	unique := make([]int64, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt <= 0 {
			return nil, ErrEventConflict
		}
		if _, found := result[attempt]; !found {
			result[attempt] = nil
			unique = append(unique, attempt)
		}
	}
	raw, err := json.Marshal(unique)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH sources AS (
			SELECT stream_uid,runner_attempt,source_event_id,ordinal,artifact_id,version_id,relation,created_at
			FROM transcript_artifact_commits WHERE stream_uid=? AND relation='produced'
			UNION ALL
			SELECT ref.stream_uid,ref.runner_attempt,ref.source_event_id,ref.ordinal,
				ref.artifact_id,ref.version_id,ref.relation,ref.created_at
			FROM transcript_artifact_refs ref
			WHERE ref.stream_uid=? AND ref.relation='produced' AND NOT EXISTS (
				SELECT 1 FROM transcript_artifact_commits committed
				WHERE committed.stream_uid=ref.stream_uid AND committed.artifact_id=ref.artifact_id
					AND committed.version_id=ref.version_id AND committed.relation='produced'
			)
		), terminals AS (
			SELECT target.attempt,target.claimed_input_revision,terminal.ordinal AS terminal_ordinal,
				state.active_branch_id
			FROM json_each(?) request
			JOIN transcript_streams stream ON stream.stream_uid=? AND stream.owner_id=?
			JOIN transcript_branch_state state ON state.stream_uid=stream.stream_uid
			JOIN transcript_runner_attempts target ON target.stream_uid=stream.stream_uid
				AND target.attempt=request.value AND target.status='completed'
			JOIN transcript_branch_events terminal ON terminal.stream_uid=stream.stream_uid
				AND terminal.branch_id=state.active_branch_id AND terminal.event_id=target.finished_event_id
		), ranked AS (
			SELECT target.attempt AS target_attempt,c.runner_attempt,c.source_event_id,c.ordinal,
				c.artifact_id,c.version_id,c.relation,c.created_at,member.ordinal AS branch_ordinal,
				ROW_NUMBER() OVER (PARTITION BY target.attempt,c.artifact_id
					ORDER BY member.ordinal DESC,c.ordinal DESC,c.version_id DESC) AS head_rank
			FROM terminals target
			JOIN transcript_runner_attempts origin ON origin.stream_uid=?
				AND origin.claimed_input_revision=target.claimed_input_revision AND origin.attempt<=target.attempt
			JOIN sources c ON c.stream_uid=origin.stream_uid AND c.runner_attempt=origin.attempt
			JOIN transcript_branch_events member ON member.stream_uid=c.stream_uid
				AND member.branch_id=target.active_branch_id AND member.event_id=c.source_event_id
				AND member.ordinal<=target.terminal_ordinal
			WHERE c.relation='produced'
		)
		SELECT c.target_attempt,c.runner_attempt,c.source_event_id,c.ordinal,
			c.artifact_id,c.version_id,c.relation,c.created_at,
			CASE WHEN tomb.version_id IS NOT NULL THEN 'deleted'
				WHEN a.id IS NULL OR v.id IS NULL THEN 'missing' ELSE 'available' END
		FROM ranked c
		JOIN transcript_streams stream ON stream.stream_uid=? AND stream.owner_id=?
		LEFT JOIN projects project ON project.id=stream.project_id AND project.user_id=stream.owner_id
		LEFT JOIN artifacts a ON a.id=c.artifact_id AND a.project_id=project.id
		LEFT JOIN artifact_versions v ON v.id=c.version_id AND v.artifact_id=a.id
		LEFT JOIN artifact_version_tombstones tomb ON tomb.artifact_id=c.artifact_id
			AND tomb.version_id=c.version_id AND tomb.owner_id=stream.owner_id AND tomb.project_id=stream.project_id
		WHERE c.head_rank=1 AND (a.id IS NULL OR a.retention_mode='snapshot')
		ORDER BY c.target_attempt,c.branch_ordinal,c.ordinal,c.artifact_id`,
		streamUID, streamUID, string(raw), streamUID, ownerID, streamUID, streamUID, ownerID)
	if err != nil {
		return nil, schemaError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var target int64
		ref := ArtifactReference{StreamUID: streamUID}
		if err := rows.Scan(&target, &ref.RunnerAttempt, &ref.SourceEventID, &ref.Ordinal,
			&ref.ArtifactID, &ref.VersionID, &ref.Relation, &ref.CreatedAt, &ref.Availability); err != nil {
			return nil, schemaError(err)
		}
		result[target] = append(result[target], ref)
	}
	return result, schemaError(rows.Err())
}
