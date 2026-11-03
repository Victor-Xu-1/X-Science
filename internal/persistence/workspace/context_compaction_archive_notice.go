package workspace

import transcriptstore "x-science/internal/persistence/transcript"

// Archive-ready notices close the original projection's durable invalidation
// handoff. Retain only an exact notice whose real archive, owner/incarnations,
// source branch, admitted control and completed canonical receipt still agree.
// Deleted/orphaned archives and prefix-only lookalikes remain reclaimable.
var canonicalCompactionArchiveNoticeRetainedSQL = `EXISTS (
 SELECT 1 FROM compaction_archives archive
 JOIN frames frame ON frame.id=archive.frame_id
 JOIN projects project ON project.id=frame.project_id
 JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
 JOIN transcript_streams scope ON scope.frame_id=frame.id AND scope.owner_id=project.user_id
 JOIN transcript_events event ON event.stream_uid=scope.stream_uid AND event.event_id=archive.source_event_id
 JOIN transcript_branch_events member ON member.stream_uid=event.stream_uid AND member.event_id=event.event_id
  AND member.branch_id=json_extract(event.payload_json,'$.maintenance.expected.branch_id')
 WHERE realtime_events.id LIKE 'context-compaction-archive:%'
  AND realtime_events.id='context-compaction-archive:'||json_extract(event.payload_json,'$.maintenance.request_id')||':'||archive.source_event_id
  AND realtime_events.user_id=project.user_id AND realtime_events.project_id=project.id
  AND realtime_events.root_frame_id=root.id AND realtime_events.frame_id=frame.id AND realtime_events.event_type='compaction_status'
  AND json_extract(realtime_events.payload,'$.schema')='x-science.context-compaction-event.v1'
  AND json_extract(realtime_events.payload,'$.state')='committed'
  AND json_extract(realtime_events.payload,'$.conversation_id')=frame.id
  AND json_extract(realtime_events.payload,'$.request_id')=json_extract(event.payload_json,'$.maintenance.request_id')
  AND json_extract(realtime_events.payload,'$.frame_incarnation_id')=frame.incarnation_id
  AND json_extract(realtime_events.payload,'$.root_frame_id')=root.id
  AND json_extract(realtime_events.payload,'$.root_frame_incarnation_id')=root.incarnation_id
  AND json_extract(event.payload_json,'$.maintenance.expected.frame_incarnation_id')=frame.incarnation_id
  AND json_extract(event.payload_json,'$.maintenance.expected.root_frame_id')=root.id
  AND json_extract(event.payload_json,'$.maintenance.expected.root_frame_incarnation_id')=root.incarnation_id
  AND (` + transcriptstore.CanonicalContextCompactionBoundarySQL() + `)
)`
