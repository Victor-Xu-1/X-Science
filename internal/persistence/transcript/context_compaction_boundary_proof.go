package transcript

import (
	"context"
	"database/sql"
	"errors"
)

// The manual branch requires the original admitted input, a completed attempt,
// and its actual terminal receipt. A checkpoint phase or copied payload alone
// cannot shorten replay. Automatic compaction retains its existing contract.
const autoCompactionBoundarySQL = `json_extract(event.payload_json,'$.toolPhase')='auto_compact'`
const compactBoundarySQL = `(` + autoCompactionBoundarySQL + ` OR (` + manualCompactionBoundarySQL + `))`

const manualCompactionBoundarySQL = `
 event.event_type='runner_checkpoint' AND event.source='payload'
 AND json_extract(event.payload_json,'$.toolPhase')='context_compact'
 AND json_extract(event.payload_json,'$.purpose')='context_compaction'
 AND json_extract(event.payload_json,'$.status')='completed'
 AND json_extract(event.payload_json,'$.compaction.schema')='x-science.context-compaction-boundary.v1'
 AND event.client_message_id='context-compaction-boundary:'||json_extract(event.payload_json,'$.maintenance.request_id')
 AND EXISTS (
 SELECT 1 FROM transcript_runner_attempts attempt
 JOIN transcript_runner_receipts receipt ON receipt.stream_uid=attempt.stream_uid AND receipt.attempt=attempt.attempt
  AND receipt.event_id=attempt.finished_event_id AND receipt.status='completed'
 JOIN transcript_events terminal ON terminal.stream_uid=attempt.stream_uid AND terminal.event_id=receipt.event_id
  AND terminal.runner_attempt=attempt.attempt AND terminal.event_type='runner_finished'
 JOIN transcript_input_admissions admission ON admission.stream_uid=attempt.stream_uid
  AND admission.input_revision=attempt.claimed_input_revision AND admission.classification_version=1
  AND admission.source_kind='event' AND admission.input_kind='message'
 JOIN transcript_events input ON input.stream_uid=admission.stream_uid AND input.event_id=admission.event_id
  AND input.source='payload' AND input.event_type='user_message' AND input.runner_attempt IS NULL
 JOIN transcript_streams stream ON stream.stream_uid=attempt.stream_uid
 WHERE attempt.stream_uid=event.stream_uid AND attempt.attempt=event.runner_attempt AND attempt.status='completed'
  AND input.publication_seq<event.publication_seq AND event.publication_seq<terminal.publication_seq
  AND json_extract(input.payload_json,'$.inputData.turn_intent')='compact-context'
  AND json_extract(input.payload_json,'$.inputData.context_compaction.schema')='x-science.context-compaction-request.v1'
  AND json_extract(input.payload_json,'$.inputData.context_compaction.request_id')=json_extract(event.payload_json,'$.maintenance.request_id')
  AND input.client_message_id='branch-continue:'||json_extract(event.payload_json,'$.maintenance.request_id')
  AND json_extract(input.payload_json,'$.messageIntent.version')=1
  AND json_extract(input.payload_json,'$.messageIntent.owner_id')=stream.owner_id
  AND json_extract(input.payload_json,'$.messageIntent.frame_id')=stream.frame_id
  AND json_extract(input.payload_json,'$.messageIntent.input_message_id')=input.client_message_id
  AND json_extract(input.payload_json,'$.messageIntent.loading_id')=json_extract(event.payload_json,'$.maintenance.request_id')
  AND json_extract(input.payload_json,'$.messageIntent.frame_incarnation_id')=json_extract(event.payload_json,'$.maintenance.expected.frame_incarnation_id')
  AND json_extract(event.payload_json,'$.maintenance.schema')='x-science.context-compaction-request.v1'
  AND json_extract(event.payload_json,'$.maintenance.expected.stream_uid')=stream.stream_uid
  AND json_extract(event.payload_json,'$.maintenance.expected.stream_epoch')=stream.epoch
  AND json_extract(event.payload_json,'$.maintenance.expected.input_revision')+1=admission.input_revision
  AND json_extract(event.payload_json,'$.maintenance.expected.branch_id')=admission.branch_id
  AND json_extract(event.payload_json,'$.maintenance.expected.branch_generation')=admission.branch_generation
  AND json_extract(event.payload_json,'$.maintenance.expected.through_publication_seq')<input.publication_seq
  AND json_extract(terminal.payload_json,'$.purpose')='context_compaction'
  AND json_extract(terminal.payload_json,'$.maintenance.schema')='x-science.context-compaction-request.v1'
  AND json_extract(terminal.payload_json,'$.maintenance.request_id')=json_extract(event.payload_json,'$.maintenance.request_id')
  AND NOT EXISTS (
   SELECT 1 FROM json_each(json_extract(event.payload_json,'$.maintenance.expected')) expected
   WHERE json_extract(input.payload_json,'$.inputData.context_compaction.expected.'||expected.key) IS NOT expected.value
    OR json_extract(terminal.payload_json,'$.maintenance.expected.'||expected.key) IS NOT expected.value
  )
 )`

// Non-serializable read-side provenance. Private fields prevent consumers from
// constructing authority from untrusted event JSON.
type ContextCompactionBoundaryProof struct{ eventID int64 }

func (proof ContextCompactionBoundaryProof) Matches(eventID int64) bool {
	return eventID > 0 && proof.eventID == eventID
}

// ReadContextCompactionBoundaryProof also serves archive final transactions.
// It uses only immutable canonical relations, not current input or generation.
func ReadContextCompactionBoundaryProof(ctx context.Context, query MessageIntentQuery, owner, stream string, eventID int64) (*ContextCompactionBoundaryProof, error) {
	installed, err := contextCompactionLedgerForStream(ctx, query, stream)
	if err != nil || !installed {
		return nil, schemaError(err)
	}
	var found int64
	err = query.QueryRowContext(ctx, `SELECT event.event_id FROM transcript_events event
  JOIN transcript_streams scope ON scope.stream_uid=event.stream_uid AND scope.owner_id=?
  WHERE event.stream_uid=? AND event.event_id=? AND (`+manualCompactionBoundarySQL+`)`, owner, stream, eventID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, schemaError(err)
	}
	return &ContextCompactionBoundaryProof{eventID: found}, nil
}

func (r *Repository) ContextCompactionBoundaryProof(ctx context.Context, owner, stream string, eventID int64) (*ContextCompactionBoundaryProof, error) {
	if r == nil || r.readDatabase() == nil {
		return nil, ErrSchemaUnavailable
	}
	return ReadContextCompactionBoundaryProof(ctx, r.readDatabase(), owner, stream, eventID)
}
