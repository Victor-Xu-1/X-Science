package transcript

import (
	"context"
	"encoding/json"
	"strings"
)

// ToolArtifactSource identifies a durable invocation, not a filename or a
// model-generated link. One batch serves a bounded visible history window.
type ToolArtifactSource struct {
	Attempt int64  `json:"attempt"`
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
}

// ToolArtifactReferences reads generation ownership directly from the commit
// ledger, including versions later bound to a final answer. It neither moves
// references nor rewrites history. Branch membership and live ownership remain
// mandatory even when a caller supplies a valid invocation from another turn.
func (r *Repository) ToolArtifactReferences(ctx context.Context, streamUID, ownerID string, sources []ToolArtifactSource) (map[ToolArtifactSource][]ArtifactReference, error) {
	result := make(map[ToolArtifactSource][]ArtifactReference)
	if len(sources) == 0 {
		return result, nil
	}
	if len(sources) > 2000 {
		return nil, ErrProjectionResourceLimit
	}
	if _, err := r.GetStream(ctx, streamUID, ownerID); err != nil {
		return nil, err
	}
	unique := make([]ToolArtifactSource, 0, len(sources))
	for _, source := range sources {
		if source.Attempt <= 0 || strings.TrimSpace(source.CallID) == "" || strings.TrimSpace(source.Name) == "" {
			return nil, ErrEventConflict
		}
		if _, seen := result[source]; seen {
			continue
		}
		result[source] = nil
		unique = append(unique, source)
	}
	raw, err := json.Marshal(unique)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.runner_attempt,json_extract(e.payload_json,'$.toolCallId'),
			json_extract(e.payload_json,'$.toolName'),c.source_event_id,c.ordinal,
			c.artifact_id,c.version_id,c.relation,c.created_at,
			CASE WHEN tomb.version_id IS NOT NULL THEN 'deleted'
				WHEN a.id IS NULL OR v.id IS NULL THEN 'missing' ELSE 'available' END
		FROM json_each(?) request
		JOIN transcript_artifact_commits c ON c.stream_uid=?
			AND c.runner_attempt=json_extract(request.value,'$.attempt')
		JOIN transcript_streams stream ON stream.stream_uid=c.stream_uid AND stream.owner_id=?
		JOIN transcript_branch_state state ON state.stream_uid=stream.stream_uid
		JOIN transcript_branch_events member ON member.stream_uid=c.stream_uid
			AND member.branch_id=state.active_branch_id AND member.event_id=c.source_event_id
		JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.source_event_id
			AND e.runner_attempt=c.runner_attempt
			AND json_extract(e.payload_json,'$.toolCallId')=json_extract(request.value,'$.call_id')
			AND json_extract(e.payload_json,'$.toolName')=json_extract(request.value,'$.name')
		LEFT JOIN projects project ON project.id=stream.project_id AND project.user_id=stream.owner_id
		LEFT JOIN artifacts a ON a.id=c.artifact_id AND a.project_id=project.id
		LEFT JOIN artifact_versions v ON v.id=c.version_id AND v.artifact_id=a.id
		LEFT JOIN artifact_version_tombstones tomb ON tomb.artifact_id=c.artifact_id
			AND tomb.version_id=c.version_id AND tomb.owner_id=stream.owner_id AND tomb.project_id=stream.project_id
		WHERE c.relation='produced' AND (a.id IS NULL OR a.retention_mode='snapshot')
			AND json_extract(e.payload_json,'$.toolPhase') IN ('start','verification_tool')
		ORDER BY member.ordinal,c.ordinal`, string(raw), streamUID, ownerID)
	if err != nil {
		return nil, schemaError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var source ToolArtifactSource
		ref := ArtifactReference{StreamUID: streamUID}
		if err := rows.Scan(&source.Attempt, &source.CallID, &source.Name, &ref.SourceEventID,
			&ref.Ordinal, &ref.ArtifactID, &ref.VersionID, &ref.Relation, &ref.CreatedAt, &ref.Availability); err != nil {
			return nil, schemaError(err)
		}
		ref.RunnerAttempt = source.Attempt
		result[source] = append(result[source], ref)
	}
	return result, schemaError(rows.Err())
}
