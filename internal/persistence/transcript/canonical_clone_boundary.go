package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
)

// Selection is a transaction-local view of the same canonical clone pipeline,
// never a new history authority or a mutation of the source conversation.
func canonicalCloneAttemptSelection(column string) string {
	return ` AND ` + column + ` IN (SELECT runner_attempt FROM ` + canonicalCloneEventTable +
		` WHERE target_uid=? AND keep=1)`
}

func scopeCanonicalCloneToReplyConn(ctx context.Context, conn *sql.Conn, source *Stream, target Stream,
	input CloneFrameHistoryInput, stage *canonicalCloneStage) error {
	if stage.legacyCutover != nil {
		return fmt.Errorf("reply branching requires canonical history: %w", ErrEventConflict)
	}
	branch := input.SourceBranchID
	if branch == "" {
		branch = stage.activeSource
	}
	var ordinal, revision int64
	err := conn.QueryRowContext(ctx, `SELECT member.ordinal,attempt.claimed_input_revision
		FROM transcript_runner_attempts attempt
		JOIN transcript_runner_receipts receipt ON receipt.stream_uid=attempt.stream_uid AND receipt.attempt=attempt.attempt
		JOIN transcript_branch_events member ON member.stream_uid=attempt.stream_uid AND member.event_id=receipt.event_id
		WHERE attempt.stream_uid=? AND attempt.attempt=? AND member.branch_id=?
		AND attempt.status='completed' AND receipt.status='completed' AND attempt.finished_event_id=receipt.event_id`,
		source.UID, input.ThroughAttempt, branch).Scan(&ordinal, &revision)
	if err == sql.ErrNoRows {
		return ErrEventConflict
	}
	if err != nil {
		return err
	}
	if ordinal <= 0 || revision <= 0 {
		return ErrEventConflict
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM `+canonicalCloneEventTable+` WHERE target_uid=? AND source_event_id NOT IN
		(SELECT event_id FROM transcript_branch_events WHERE stream_uid=? AND branch_id=? AND ordinal<=?)`,
		target.UID, source.UID, branch, ordinal); err != nil {
		return err
	}
	// Negative temporary ordinals avoid collisions with the UNIQUE target-event key.
	if _, err := conn.ExecContext(ctx, `WITH numbered AS (SELECT source_key,ROW_NUMBER() OVER(ORDER BY target_event_id) AS n
		FROM `+canonicalCloneEventTable+` WHERE target_uid=? AND keep=1)
		UPDATE `+canonicalCloneEventTable+` SET target_event_id=-(SELECT n FROM numbered WHERE numbered.source_key=synon_clone_event_map.source_key)
		WHERE target_uid=? AND keep=1`, target.UID, target.UID); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE `+canonicalCloneEventTable+` SET target_event_id=-target_event_id WHERE target_uid=? AND keep=1`, target.UID); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT source_key,client_message_id,event_type,payload_json,target_event_id
		FROM `+canonicalCloneEventTable+` WHERE target_uid=? AND keep=1 ORDER BY target_event_id`, target.UID)
	if err != nil {
		return err
	}
	digest := sha256.New()
	count := 0
	for rows.Next() {
		var key, client, kind string
		var payload []byte
		var publication int64
		if err := rows.Scan(&key, &client, &kind, &payload, &publication); err != nil {
			rows.Close()
			return err
		}
		count++
		writeCanonicalCloneEventDigest(digest, key, client, kind, payload, publication)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	stage.eventDigest, stage.eventCount = digest.Sum(nil), count
	stage.activeSource, stage.activeTarget, stage.branchGeneration = branch, branch, 1
	stage.throughOrdinal = ordinal
	source.InputRevision, source.ConsumedInputRevision = revision, revision
	return conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(checkpoint_sequence),0)+1 FROM transcript_runner_checkpoints
		WHERE stream_uid=?`+canonicalCloneAttemptSelection("runner_attempt"), source.UID, target.UID).Scan(&source.NextCheckpoint)
}

func scopedCanonicalCloneCountsConn(ctx context.Context, conn *sql.Conn, sourceUID, targetUID string) (canonicalCloneSourceCounts, error) {
	var counts canonicalCloneSourceCounts
	queries := []struct {
		table, column string
		count         *int
	}{
		{"transcript_runner_attempts", "attempt", &counts.attempts},
		{"transcript_runner_receipts", "attempt", &counts.receipts},
		{"transcript_runner_checkpoints", "runner_attempt", &counts.checkpoints},
	}
	for _, query := range queries {
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+query.table+` WHERE stream_uid=?`+
			canonicalCloneAttemptSelection(query.column), sourceUID, targetUID).Scan(query.count); err != nil {
			return counts, err
		}
	}
	return counts, nil
}
