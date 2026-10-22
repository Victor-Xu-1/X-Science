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
	input, err := r.ClaimedRunnerInputContext(ctx, claim)
	if err != nil || input == nil {
		return nil, err
	}
	return input.InputData, nil
}

// ClaimedInputContext binds composer data and its source boundary to the same
// admitted input. Callers must not recover that identity from the newest replay
// message: a later input may already be queued while this claim is running.
type ClaimedInputContext struct {
	EventID         int64
	PublicationSeq  int64
	ClientMessageID string
	InputData       map[string]any
	Association     *ContextCallAssociation
}

func (r *Repository) ClaimedRunnerInputContext(ctx context.Context, claim RunnerClaim) (*ClaimedInputContext, error) {
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
	input, err := readAdmittedRunnerInputContextConn(ctx, conn, stream, claim.ClaimedInputRevision, false)
	if err == nil && input != nil && input.Association != nil {
		input.Association.RunnerAttempt = claim.Attempt
	}
	return input, err
}

// RunnerInputContext reads the admitted ordinary input for one recorded attempt.
// It is read-only provenance, not permission to execute or mutate anything.
// ActiveRunnerInputContext below additionally excludes terminal attempts.
func (r *Repository) RunnerInputContext(ctx context.Context, streamUID, ownerID string, attempt int64) (*ClaimedInputContext, error) {
	if attempt <= 0 {
		return nil, ErrEventConflict
	}
	return r.readRunnerInputContext(ctx, streamUID, ownerID, attempt, false)
}

func (r *Repository) ActiveRunnerInputContext(ctx context.Context, streamUID, ownerID string) (*ClaimedInputContext, error) {
	return r.readRunnerInputContext(ctx, streamUID, ownerID, 0, true)
}

func (r *Repository) readRunnerInputContext(ctx context.Context, streamUID, ownerID string, attempt int64, activeOnly bool) (*ClaimedInputContext, error) {
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
	stream, err := getStreamConn(ctx, conn, streamUID, ownerID)
	if err != nil {
		return nil, schemaError(err)
	}
	var revision int64
	var status string
	query := `SELECT claimed_input_revision,status FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`
	args := []any{stream.UID, attempt}
	if activeOnly {
		query = `SELECT claimed_input_revision,status FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`
		args = []any{stream.UID}
	}
	err = conn.QueryRowContext(ctx, query, args...).Scan(&revision, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, schemaError(err)
	}
	if activeOnly && status != "running" {
		return nil, nil
	}
	return readAdmittedRunnerInputContextConn(ctx, conn, stream, revision, !activeOnly)
}

func readAdmittedRunnerInputContextConn(ctx context.Context, conn *sql.Conn, stream Stream, inputRevision int64, historical bool) (*ClaimedInputContext, error) {
	var branch string
	var generation int64
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state
		WHERE stream_uid=?`, stream.UID).Scan(&branch, &generation); err != nil {
		return nil, schemaError(err)
	}
	var factBranch, eventType, clientID, source string
	var version, factGeneration, admittedRevision int64
	var runnerAttempt sql.NullInt64
	var raw []byte
	var member bool
	var input ClaimedInputContext
	err := conn.QueryRowContext(ctx, `SELECT a.classification_version,a.branch_id,a.branch_generation,
		e.event_type,e.client_message_id,e.source,e.runner_attempt,e.event_id,e.publication_seq,a.input_revision,
		CASE WHEN e.source='payload' THEN e.payload_json ELSE f.payload END,
		EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=a.stream_uid AND m.branch_id=a.branch_id AND m.event_id=a.event_id)
		FROM transcript_input_admissions a JOIN transcript_events e ON e.stream_uid=a.stream_uid AND e.event_id=a.event_id
		LEFT JOIN frame_events f ON e.source='frame_ref' AND f.id=e.frame_event_id AND f.frame_id=? AND f.event_type=e.event_type
		WHERE a.stream_uid=? AND a.source_kind='event' AND a.input_kind='message' AND a.input_revision<=?
			AND EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=a.stream_uid AND m.branch_id=? AND m.event_id=a.event_id)
		ORDER BY a.input_revision DESC LIMIT 1`, stream.FrameID, stream.UID, inputRevision, branch).
		Scan(&version, &factBranch, &factGeneration, &eventType, &clientID, &source, &runnerAttempt, &input.EventID, &input.PublicationSeq, &admittedRevision, &raw, &member)
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
	// A continuation rotates generation even without switching branches. Read-
	// only historical attribution may retain an older same-branch admission,
	// but only with its immutable classification and exact branch membership.
	// Live claimed-input reads never receive this relaxation.
	if historical && factGeneration > 0 && factGeneration < generation {
		validGeneration = true
	}
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
	input.ClientMessageID = clientID
	if !historical && stream.Kind == StreamKindFrameRef {
		scope, err := contextInspectionScopeConn(ctx, conn, stream)
		if err != nil {
			return nil, schemaError(err)
		}
		input.Association = &ContextCallAssociation{Version: 1, Scope: scope, InputMessageID: contextInputMessageID(raw, clientID),
			InputEventID: input.EventID, InputRevision: admittedRevision, InputPublicationSequence: input.PublicationSeq,
			AdmissionBranchID: factBranch, AdmissionBranchGeneration: factGeneration, ClaimedInputRevision: inputRevision}
	}
	if !found {
		return &input, nil
	}
	var inputData map[string]any
	if err := json.Unmarshal(data, &inputData); err != nil || inputData == nil {
		return nil, ErrEventConflict
	}
	input.InputData = inputData
	return &input, nil
}
