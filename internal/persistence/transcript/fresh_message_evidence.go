package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// FreshMessageEvidence proves new ordinary input, not complete historical
// coverage or absence of an old operation's side effects. Missing mappings stay
// missing. Every present fact is verified before either proof can authorize work.
type FreshMessageEvidence struct {
	Coverage        InputCoverage
	Authorized      bool
	Source          string
	BoundaryEventID int64
	InputEventID    int64
}

func (r *Repository) freshMessageEvidenceConn(ctx context.Context, conn *sql.Conn, stream Stream) (FreshMessageEvidence, error) {
	var attempt, claimed int64
	var status string
	var finished sql.NullInt64
	err := conn.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision,status,finished_event_id
		FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID).Scan(&attempt, &claimed, &status, &finished)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FreshMessageEvidence{}, err
	}
	found := err == nil
	coverage, err := r.inputCoverageConn(ctx, conn, stream, claimed)
	result := FreshMessageEvidence{Coverage: coverage}
	if err != nil || !coverage.Pending() {
		return result, err
	}
	if coverage.HasMessage {
		answers, err := waitingCheckpointAcceptsPendingAnswersConn(ctx, conn, stream, attempt, claimed, coverage)
		if err != nil || answers {
			return result, err
		}
		result.Authorized, result.Source = true, "exact_admission"
		return result, nil
	}
	if coverage.Complete || !found || stream.ConsumedInputRevision > claimed || status == "cancelled" {
		return result, nil
	}
	if status != "completed" {
		var waitPayload string
		err := conn.QueryRowContext(ctx, `SELECT e.payload_json FROM transcript_runner_checkpoints c
			JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.event_id
			WHERE c.stream_uid=? AND c.runner_attempt=? ORDER BY c.checkpoint_sequence DESC LIMIT 1`, stream.UID, attempt).Scan(&waitPayload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if err == nil && !runnerCheckpointAllowsAutomaticResume(waitPayload) {
			return result, nil
		}
	}
	var branch string
	if err := conn.QueryRowContext(ctx, `SELECT active_branch_id FROM transcript_branch_state WHERE stream_uid=?`, stream.UID).Scan(&branch); err != nil {
		return result, err
	}
	// Prefer the actual finished receipt boundary, not terminal-looking prose.
	if finished.Valid {
		var boundary int64
		var terminalPayload []byte
		err := conn.QueryRowContext(ctx, `SELECT e.event_id,e.payload_json FROM transcript_runner_receipts receipt
			JOIN transcript_events e ON e.stream_uid=receipt.stream_uid AND e.event_id=receipt.event_id AND e.runner_attempt=receipt.attempt
			JOIN transcript_branch_events m ON m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=?
			WHERE receipt.stream_uid=? AND receipt.attempt=? AND receipt.event_id=? AND receipt.status=?
		AND e.event_type='runner_finished' AND e.source='payload' AND e.frame_event_id IS NULL`, branch, stream.UID, attempt, finished.Int64, status).Scan(&boundary, &terminalPayload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if err == nil {
			if !terminalStatus(status) || !json.Valid(terminalPayload) {
				return result, ErrEventConflict
			}
			eventID, err := legacyOrdinaryAfterBoundaryConn(ctx, conn, stream, branch, boundary)
			if err != nil {
				return result, err
			}
			if eventID > 0 {
				result.Authorized, result.Source, result.BoundaryEventID, result.InputEventID = true, "legacy_finish", boundary, eventID
				return result, nil
			}
		}
	}
	var boundary int64
	var payload []byte
	err = conn.QueryRowContext(ctx, `SELECT e.event_id,e.payload_json FROM transcript_events e
		JOIN transcript_branch_events m ON m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=?
		WHERE e.stream_uid=? AND e.runner_attempt=? AND e.event_type=? AND e.source='payload' AND e.frame_event_id IS NULL
		AND e.client_message_id=?`, branch, stream.UID, attempt, RunnerPreparationAdmissionEventType,
		"runner-preparation:"+stream.UID+":"+strconv.FormatInt(attempt, 10)).Scan(&boundary, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var preparation struct {
		Revision int    `json:"revision"`
		State    string `json:"dispatch_state"`
		Claimed  int64  `json:"claimed_input_revision"`
	}
	if json.Unmarshal(payload, &preparation) != nil || preparation.Revision != 1 || preparation.State != "preparation" || preparation.Claimed != claimed {
		return result, nil
	}
	eventID, err := legacyOrdinaryAfterBoundaryConn(ctx, conn, stream, branch, boundary)
	if err != nil {
		return result, err
	}
	if eventID > 0 {
		result.Authorized, result.Source, result.BoundaryEventID, result.InputEventID = true, "legacy_preparation", boundary, eventID
	}
	return result, nil
}

func legacyOrdinaryAfterBoundaryConn(ctx context.Context, conn *sql.Conn, stream Stream, branch string, boundary int64) (int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT e.event_id,e.event_type,e.source,e.client_message_id,e.runner_attempt,
		e.frame_event_id,CASE WHEN e.source='payload' THEN e.payload_json ELSE f.payload END
		FROM transcript_events e JOIN transcript_branch_events m ON m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=?
		LEFT JOIN frame_events f ON e.source='frame_ref' AND f.id=e.frame_event_id AND f.frame_id=? AND f.event_type=e.event_type
		WHERE e.stream_uid=? AND e.event_id>? AND e.event_type IN ('user_message','user_input_response')
		ORDER BY e.event_id`, branch, stream.FrameID, stream.UID, boundary)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind, source, client string
		var runner sql.NullInt64
		var frameEvent sql.NullString
		var payload []byte
		if err := rows.Scan(&id, &kind, &source, &client, &runner, &frameEvent, &payload); err != nil {
			return 0, err
		}
		if runner.Valid || (source != string(EventSourcePayload) && source != string(EventSourceFrameRef)) || len(payload) == 0 || !json.Valid(payload) {
			return 0, ErrEventConflict
		}
		if source == string(EventSourcePayload) && frameEvent.Valid || source == string(EventSourceFrameRef) && (stream.Kind != StreamKindFrameRef || !frameEvent.Valid || frameEvent.String == "") {
			return 0, ErrEventConflict
		}
		inputKind, err := admittedEventBytesKind(kind, client, payload)
		if err != nil {
			return 0, err
		}
		if inputKind == "message" {
			return id, nil
		}
	}
	return 0, rows.Err()
}

func (r *Repository) GetFreshMessageEvidence(ctx context.Context, uid, owner string) (FreshMessageEvidence, error) {
	if r == nil || r.readDatabase() == nil {
		return FreshMessageEvidence{}, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return FreshMessageEvidence{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return FreshMessageEvidence{}, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := getStreamConn(ctx, conn, uid, owner)
	if err != nil {
		return FreshMessageEvidence{}, err
	}
	return r.freshMessageEvidenceConn(ctx, conn, stream)
}

// Candidate index only. Coarse legacy shapes may include a control answer;
// bounded keyset iteration skips it and the source reader remains authoritative.
func freshMessageCandidateSQL(stream, latest string) string {
	lower := "MAX(" + stream + ".consumed_input_revision," + latest + ".claimed_input_revision)"
	return "(" + stream + ".input_revision>" + lower + " AND (EXISTS(SELECT 1 FROM transcript_input_admissions a WHERE a.stream_uid=" + stream +
		".stream_uid AND a.input_revision>" + lower + " AND a.input_revision<=" + stream + ".input_revision AND a.input_kind='message') OR (" +
		stream + ".consumed_input_revision<=" + latest + ".claimed_input_revision AND EXISTS(SELECT 1 FROM transcript_events marker " +
		"JOIN transcript_branch_state b ON b.stream_uid=marker.stream_uid JOIN transcript_branch_events m ON m.stream_uid=b.stream_uid AND m.branch_id=b.active_branch_id AND m.event_id=marker.event_id " +
		"WHERE marker.stream_uid=" + stream + ".stream_uid AND marker.runner_attempt=" + latest + ".attempt AND marker.event_type IN ('runner_preparation_admitted','runner_finished') " +
		"AND EXISTS(SELECT 1 FROM transcript_events input JOIN transcript_branch_events member ON member.stream_uid=input.stream_uid AND member.event_id=input.event_id AND member.branch_id=b.active_branch_id " +
		"WHERE input.stream_uid=marker.stream_uid AND input.event_id>marker.event_id AND input.runner_attempt IS NULL AND input.event_type IN ('user_message','user_input_response'))))))"
}
