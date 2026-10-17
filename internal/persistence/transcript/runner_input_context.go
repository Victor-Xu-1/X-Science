package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ClaimedRunnerInputData reads turn-scoped composer data from the newest
// ordinary input actually admitted to this claim. Publication order and the
// newest queued input are not execution authority. Control/recovery inputs
// retain the preceding ordinary input; a new ordinary input replaces it,
// including clearing selections when inputData is absent.
//
// The read shares one snapshot with live claim, owner, active stream and branch
// validation. It never updates a compatibility session or manufactures missing
// historical admission facts.
func (r *Repository) ClaimedRunnerInputData(ctx context.Context, claim RunnerClaim) (map[string]any, error) {
	if r == nil || r.readDatabase() == nil {
		return nil, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := validateClaimConn(ctx, conn, claim, r.now().UTC(), true)
	if err != nil {
		return nil, schemaError(err)
	}
	var branch string
	var generation int64
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state
		WHERE stream_uid=?`, stream.UID).Scan(&branch, &generation); err != nil {
		return nil, schemaError(err)
	}
	var factBranch, eventType, clientID, source string
	var version, factGeneration int64
	var runnerAttempt sql.NullInt64
	var raw []byte
	var member bool
	err = conn.QueryRowContext(ctx, `SELECT a.classification_version,a.branch_id,a.branch_generation,
		e.event_type,e.client_message_id,e.source,e.runner_attempt,
		CASE WHEN e.source='payload' THEN e.payload_json ELSE f.payload END,
		EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=a.stream_uid AND m.branch_id=a.branch_id AND m.event_id=a.event_id)
		FROM transcript_input_admissions a JOIN transcript_events e ON e.stream_uid=a.stream_uid AND e.event_id=a.event_id
		LEFT JOIN frame_events f ON e.source='frame_ref' AND f.id=e.frame_event_id AND f.frame_id=? AND f.event_type=e.event_type
		WHERE a.stream_uid=? AND a.source_kind='event' AND a.input_kind='message' AND a.input_revision<=?
			AND EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=a.stream_uid AND m.branch_id=? AND m.event_id=a.event_id)
		ORDER BY a.input_revision DESC LIMIT 1`, stream.FrameID, stream.UID, claim.ClaimedInputRevision, branch).
		Scan(&version, &factBranch, &factGeneration, &eventType, &clientID, &source, &runnerAttempt, &raw, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // A copied/legacy prefix has no prospective admission proof.
	}
	if err != nil {
		return nil, schemaError(err)
	}
	// An answered branch may retain an admitted ancestor in its shared prefix.
	// Its original admission is not rewritten; sibling/nonmember inputs cannot
	// enter the query, and an invalid same-branch generation is never accepted.
	validGeneration := factGeneration == generation && factBranch == branch || factGeneration > 0 && factGeneration < generation && factBranch != branch
	if version != AdmissionClassificationV1 || !validGeneration || !member ||
		runnerAttempt.Valid || (source != string(EventSourcePayload) && source != string(EventSourceFrameRef)) ||
		len(raw) == 0 || len(raw) > maxEventPayloadBytes {
		return nil, ErrEventConflict
	}
	kind, err := admittedEventBytesKind(eventType, clientID, raw)
	if err != nil || kind != "message" {
		return nil, ErrEventConflict
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, ErrEventConflict
	}
	data, found := payload["inputData"]
	if !found {
		return nil, nil
	}
	var inputData map[string]any
	if err := json.Unmarshal(data, &inputData); err != nil || inputData == nil {
		return nil, ErrEventConflict
	}
	return inputData, nil
}
