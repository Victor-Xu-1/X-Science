package transcript

import (
	"context"
	"database/sql"
	"errors"
)

// An ordinary composer input can answer the current question without replacing
// its provider continuation. Admission remains a message fact: only Fresh
// handoff authority changes, and only for a fully verified pending interval.
func waitingCheckpointAcceptsPendingAnswersConn(
	ctx context.Context, conn *sql.Conn, stream Stream, attempt, claimed int64, coverage InputCoverage,
) (bool, error) {
	if stream.Kind != StreamKindFrameRef || !coverage.Complete || !coverage.HasMessage ||
		stream.ConsumedInputRevision != claimed {
		return false, nil
	}
	var boundary int64
	err := conn.QueryRowContext(ctx, `SELECT e.event_id
		FROM transcript_runner_attempts a
		JOIN transcript_runner_checkpoints c ON c.stream_uid=a.stream_uid AND c.runner_attempt=a.attempt
			AND c.checkpoint_sequence=a.last_checkpoint_sequence AND c.phase=a.phase AND c.resumable=1
		JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.event_id
			AND e.runner_attempt=a.attempt AND e.event_type='runner_checkpoint' AND e.source='payload'
		JOIN transcript_branch_state b ON b.stream_uid=a.stream_uid
		JOIN transcript_branch_events m ON m.stream_uid=b.stream_uid AND m.branch_id=b.active_branch_id AND m.event_id=e.event_id
		WHERE a.stream_uid=? AND a.attempt=? AND a.status='running' AND a.phase IN ('waiting_user','waiting_approval')`,
		stream.UID, attempt).Scan(&boundary)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// inputCoverageConn already verified each present kind against its exact
	// source bytes and current branch. Do not reinterpret the stored kind, infer
	// a missing revision, or use a latest-event heuristic: every ordinary source
	// in this complete interval must answer this exact checkpoint boundary.
	var otherMessage bool
	err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_input_admissions a
		JOIN transcript_events e ON e.stream_uid=a.stream_uid AND e.event_id=a.event_id
		WHERE a.stream_uid=? AND a.input_revision>? AND a.input_revision<=? AND a.input_kind='message'
		AND (e.event_type!='user_input_response' OR e.event_id<=?))`,
		stream.UID, coverage.AfterRevision, coverage.InputRevision, boundary).Scan(&otherMessage)
	return !otherMessage && err == nil, err
}
