package transcript

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// InputCoverage reports admission, not whether a provider read or answered it.
// Missing old facts are recoverable unknown history. Contradictory existing
// facts return an error and cannot authorize an ordinary continuation.
type InputCoverage struct {
	AfterRevision         int64
	InputRevision         int64
	ConsumedInputRevision int64
	Complete              bool
	HasMessage            bool
	HasControl            bool
	HasRecovery           bool
}

func (c InputCoverage) Pending() bool { return c.InputRevision > c.AfterRevision }

// RecoveryWork requires a real recovery source. Control answers may accompany
// that source but never independently authorize retry; ordinary new messages
// use their existing fresh-input path instead.
func (c InputCoverage) RecoveryWork() bool {
	return c.Pending() && c.Complete && c.HasRecovery && !c.HasMessage
}

func (r *Repository) inputCoverageConn(ctx context.Context, conn *sql.Conn, stream Stream, after int64) (InputCoverage, error) {
	if after < 0 || after > stream.InputRevision || stream.ConsumedInputRevision > stream.InputRevision {
		return InputCoverage{}, ErrEventConflict
	}
	after = max(after, stream.ConsumedInputRevision)
	coverage := InputCoverage{AfterRevision: after, InputRevision: stream.InputRevision,
		ConsumedInputRevision: stream.ConsumedInputRevision, Complete: true}
	if !coverage.Pending() {
		return coverage, nil
	}
	var branchID string
	var generation int64
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state
		WHERE stream_uid=?`, stream.UID).Scan(&branchID, &generation); err != nil {
		return coverage, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT a.input_revision,a.source_kind,a.input_kind,a.classification_version,
		a.branch_id,a.branch_generation,a.event_id,a.operation_id,a.operation_state_version,
		COALESCE(e.event_type,''),COALESCE(e.source,''),COALESCE(e.client_message_id,''),e.runner_attempt,
		CASE WHEN e.source='payload' THEN e.payload_json ELSE f.payload END,
		EXISTS(SELECT 1 FROM transcript_branch_events m WHERE m.stream_uid=a.stream_uid AND m.branch_id=? AND m.event_id=a.event_id)
		FROM transcript_input_admissions a LEFT JOIN transcript_events e ON e.stream_uid=a.stream_uid AND e.event_id=a.event_id
		LEFT JOIN frame_events f ON e.source='frame_ref' AND f.id=e.frame_event_id AND f.frame_id=? AND f.event_type=e.event_type
		WHERE a.stream_uid=? AND a.input_revision>? AND a.input_revision<=? ORDER BY a.input_revision`,
		branchID, stream.FrameID, stream.UID, after, stream.InputRevision)
	if err != nil {
		return coverage, err
	}
	defer rows.Close()
	expected := after + 1
	for rows.Next() {
		var revision, version, factGeneration int64
		var sourceKind, kind, factBranch, eventType, source, clientID string
		var eventID, stateVersion, runnerAttempt sql.NullInt64
		var operationID sql.NullString
		var payload []byte
		var member bool
		if err := rows.Scan(&revision, &sourceKind, &kind, &version, &factBranch, &factGeneration,
			&eventID, &operationID, &stateVersion, &eventType, &source, &clientID, &runnerAttempt, &payload, &member); err != nil {
			return coverage, err
		}
		if revision != expected {
			coverage.Complete = false
		}
		expected = revision + 1
		if version != AdmissionClassificationV1 || factBranch != branchID || factGeneration != generation {
			return coverage, fmt.Errorf("input admission authority differs: %w", ErrEventConflict)
		}
		switch sourceKind {
		case "event":
			if !eventID.Valid || eventID.Int64 <= 0 || operationID.Valid || stateVersion.Valid || runnerAttempt.Valid ||
				!member || (source != string(EventSourcePayload) && source != string(EventSourceFrameRef)) || len(payload) == 0 {
				return coverage, fmt.Errorf("input admission event differs: %w", ErrEventConflict)
			}
			expectedKind, err := admittedEventBytesKind(eventType, clientID, payload)
			if err != nil {
				return coverage, err
			}
			if kind != expectedKind {
				return coverage, fmt.Errorf("input admission classification differs: %w", ErrEventConflict)
			}
			coverage.HasMessage = coverage.HasMessage || kind == "message"
			coverage.HasControl = coverage.HasControl || kind == "control"
		case "kernel_recovery":
			if eventID.Valid || !operationID.Valid || operationID.String == "" || !stateVersion.Valid || stateVersion.Int64 <= 0 || kind != "recovery" {
				return coverage, ErrEventConflict
			}
			if r == nil || r.recoveryAdmissionVerifier == nil {
				return coverage, ErrSchemaUnavailable
			}
			if err := r.recoveryAdmissionVerifier(ctx, conn, RecoveryAdmissionSource{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, ProjectID: stream.ProjectID, FrameID: stream.FrameID,
				Epoch: stream.Epoch, BranchID: branchID, BranchGeneration: generation, InputRevision: revision,
				OperationID: operationID.String, StateVersion: stateVersion.Int64,
			}); err != nil {
				return coverage, err
			}
			coverage.HasRecovery = true
		default:
			return coverage, ErrEventConflict
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return coverage, err
	}
	coverage.Complete = coverage.Complete && expected == stream.InputRevision+1
	return coverage, nil
}

func (tx *ImmediateTransaction) InputCoverage(ctx context.Context, streamUID, ownerID string, after int64) (InputCoverage, error) {
	if tx == nil || tx.conn == nil {
		return InputCoverage{}, ErrSchemaUnavailable
	}
	stream, err := getStreamConn(ctx, tx.conn, streamUID, ownerID)
	if err != nil {
		return InputCoverage{}, err
	}
	return tx.repository.inputCoverageConn(ctx, tx.conn, stream, after)
}

func (r *Repository) GetInputCoverage(ctx context.Context, streamUID, ownerID string, after int64) (InputCoverage, error) {
	if r == nil || r.readDatabase() == nil {
		return InputCoverage{}, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return InputCoverage{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return InputCoverage{}, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := getStreamConn(ctx, conn, streamUID, ownerID)
	if err != nil {
		return InputCoverage{}, err
	}
	return r.inputCoverageConn(ctx, conn, stream, after)
}

// This is a candidate index only. Every selected stream is revalidated by
// inputCoverageConn before it obtains execution authority. The persisted kind
// comes only from the private versioned source-byte constructor.
func pendingInputKindSQL(streamAlias, afterExpression string, message bool) string {
	lower := "MAX(" + streamAlias + ".consumed_input_revision," + afterExpression + ")"
	interval := "a.stream_uid=" + streamAlias + ".stream_uid AND a.input_revision>" + lower + " AND a.input_revision<=" + streamAlias + ".input_revision"
	hasMessage := "EXISTS(SELECT 1 FROM transcript_input_admissions a WHERE " + interval + " AND a.input_kind='message')"
	if !message {
		hasMessage = "NOT " + hasMessage
	}
	return "(" + streamAlias + ".input_revision>" + lower + " AND (SELECT COUNT(*) FROM transcript_input_admissions a WHERE " + interval + ")=" +
		streamAlias + ".input_revision-" + lower + " AND " + hasMessage + ")"
}

func pendingRecoveryInputSQL(streamAlias, afterExpression string) string {
	lower := "MAX(" + streamAlias + ".consumed_input_revision," + afterExpression + ")"
	return "(" + pendingInputKindSQL(streamAlias, afterExpression, false) +
		" AND EXISTS(SELECT 1 FROM transcript_input_admissions a WHERE a.stream_uid=" + streamAlias +
		".stream_uid AND a.input_revision>" + lower + " AND a.input_revision<=" + streamAlias + ".input_revision AND a.input_kind='recovery'))"
}
