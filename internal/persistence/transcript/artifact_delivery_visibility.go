package transcript

// ArtifactGenerationSourcesSQL is the durable generation authority shared by
// completed-round attachments and collection visibility. Typed legacy receipts
// are used only when that immutable version has no commit-ledger entry.
const ArtifactGenerationSourcesSQL = `
 SELECT stream_uid,runner_attempt,source_event_id,ordinal,artifact_id,version_id,relation,created_at
 FROM transcript_artifact_commits WHERE relation='produced'
 UNION ALL
 SELECT ref.stream_uid,ref.runner_attempt,ref.source_event_id,ref.ordinal,
  ref.artifact_id,ref.version_id,ref.relation,ref.created_at
 FROM transcript_artifact_refs ref
 WHERE ref.relation='produced' AND NOT EXISTS (
  SELECT 1 FROM transcript_artifact_commits committed
  WHERE committed.stream_uid=ref.stream_uid AND committed.artifact_id=ref.artifact_id
   AND committed.version_id=ref.version_id AND committed.relation='produced'
 )`

// CompletedArtifactVersionVisibilitySQL is a read-only recovery projection for
// old snapshot drafts. It expects artifact/version aliases a/v. Only a latest
// generated head on the active branch, through a completed terminal of the same
// admitted input, is delivered. It never publishes by filename, model prose,
// frame status, or the existence of unrelated later work. Original provenance
// flags and bytes remain untouched; uncompleted and abandoned drafts stay hidden.
const CompletedArtifactVersionVisibilitySQL = `EXISTS (
 WITH sources AS NOT MATERIALIZED (` + ArtifactGenerationSourcesSQL + `)
 SELECT 1 FROM sources generated
 JOIN transcript_streams stream ON stream.stream_uid=generated.stream_uid
 JOIN projects owned ON owned.id=stream.project_id AND owned.user_id=stream.owner_id
 JOIN transcript_branch_state state ON state.stream_uid=stream.stream_uid
 JOIN transcript_runner_attempts origin ON origin.stream_uid=stream.stream_uid
  AND origin.attempt=generated.runner_attempt
 JOIN transcript_runner_attempts terminal ON terminal.stream_uid=origin.stream_uid
  AND terminal.claimed_input_revision=origin.claimed_input_revision
  AND terminal.attempt>=origin.attempt AND terminal.status='completed'
 JOIN transcript_branch_events boundary ON boundary.stream_uid=terminal.stream_uid
  AND boundary.branch_id=state.active_branch_id AND boundary.event_id=terminal.finished_event_id
 JOIN transcript_branch_events member ON member.stream_uid=generated.stream_uid
  AND member.branch_id=state.active_branch_id AND member.event_id=generated.source_event_id
 WHERE a.retention_mode='snapshot' AND a.project_id=owned.id
  AND generated.artifact_id=a.id AND generated.version_id=v.id AND generated.relation='produced'
  AND member.ordinal<=boundary.ordinal AND NOT EXISTS (
   SELECT 1 FROM sources newer
   JOIN transcript_runner_attempts next_origin ON next_origin.stream_uid=newer.stream_uid
    AND next_origin.attempt=newer.runner_attempt
   JOIN transcript_branch_events next_member ON next_member.stream_uid=newer.stream_uid
    AND next_member.branch_id=state.active_branch_id AND next_member.event_id=newer.source_event_id
   WHERE newer.stream_uid=generated.stream_uid AND newer.artifact_id=generated.artifact_id
    AND newer.relation='produced' AND next_origin.claimed_input_revision=terminal.claimed_input_revision
    AND newer.runner_attempt<=terminal.attempt AND next_member.ordinal<=boundary.ordinal
    AND (next_member.ordinal>member.ordinal OR (next_member.ordinal=member.ordinal
     AND (newer.ordinal>generated.ordinal OR (newer.ordinal=generated.ordinal AND newer.version_id>generated.version_id))))
  )
)`
