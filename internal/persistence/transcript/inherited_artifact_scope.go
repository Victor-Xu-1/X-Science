package transcript

import (
	"context"
	"database/sql"
)

// A clone may read only the exact versions durably included in its copied
// branch prefix. A source-root shortcut would expose future parent versions.
// InheritedArtifactScopeCTE is shared by canonical copy validation and workspace
// message metadata, export and paginated reads (parameters: owner, project, frame).
// It does not transfer artifact mutation ownership to the child. Retained
// inactive branches remain readable, just like original owned history; selected
// prefix staging (not the current UI branch) governs a subsequent clone.
const InheritedArtifactScopeCTE = `
 artifact_scope(owner_id,project_id,frame_id) AS (VALUES (?,?,?)),
 inherited_stream AS (
  SELECT stream.stream_uid,receipt.source_event_count
  FROM artifact_scope scope
  JOIN transcript_frame_authority authority ON authority.owner_id=scope.owner_id AND authority.session_id=scope.frame_id
  JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid
   AND stream.owner_id=scope.owner_id AND stream.project_id=scope.project_id AND stream.epoch=authority.active_epoch
  JOIN transcript_payload_genesis_receipts receipt ON receipt.genesis_id=authority.genesis_id
   AND receipt.stream_uid=stream.stream_uid AND receipt.epoch=stream.epoch
   AND receipt.owner_id=stream.owner_id AND receipt.source_kind='canonical_clone' AND receipt.status='active'
 ), inherited_versions AS (
  SELECT ref.artifact_id,ref.version_id,ref.relation FROM inherited_stream stream
  JOIN transcript_artifact_commits ref ON ref.stream_uid=stream.stream_uid AND ref.source_event_id<=stream.source_event_count
  JOIN transcript_branch_events member ON member.stream_uid=ref.stream_uid
   AND member.event_id=ref.source_event_id
  UNION
  SELECT ref.artifact_id,ref.version_id,ref.relation FROM inherited_stream stream
  JOIN transcript_artifact_refs ref ON ref.stream_uid=stream.stream_uid AND ref.source_event_id<=stream.source_event_count
  JOIN transcript_branch_events member ON member.stream_uid=ref.stream_uid
   AND member.event_id=ref.source_event_id
 ), inherited_heads AS (
  SELECT ref.artifact_id,MAX(version.version_number) AS version_number FROM inherited_versions ref
  JOIN artifact_versions version ON version.id=ref.version_id AND version.artifact_id=ref.artifact_id
  GROUP BY ref.artifact_id
 )`

// An empty relation permits a new read/citation of inherited evidence. Copy
// validation supplies the original relation to preserve its historical fact.
func inheritedArtifactVersionConn(ctx context.Context, conn *sql.Conn, source Stream, artifactID, versionID string, relation ArtifactRelation) (bool, error) {
	var inherited bool
	err := conn.QueryRowContext(ctx, `WITH `+InheritedArtifactScopeCTE+`
	 SELECT EXISTS (SELECT 1 FROM inherited_versions ref
	 JOIN artifact_versions version ON version.id=ref.version_id AND version.artifact_id=ref.artifact_id
	 JOIN artifacts artifact ON artifact.id=ref.artifact_id
	 JOIN projects project ON project.id=artifact.project_id
	 CROSS JOIN artifact_scope scope
	 WHERE artifact.project_id=scope.project_id AND project.user_id=scope.owner_id
	  AND ref.artifact_id=? AND ref.version_id=? AND (?='' OR ref.relation=?))`,
		source.OwnerID, source.ProjectID, source.SessionID, artifactID, versionID, relation, relation).Scan(&inherited)
	return inherited, err
}
