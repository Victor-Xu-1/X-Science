package transcript

import (
	"context"
	"encoding/json"
	"time"
)

type CompletedRoundUsageAuthority struct {
	Attempt       int64
	InputRevision int64
	CompletedAt   time.Time
	Elapsed       time.Duration
	Attempts      []int64
	AuditSessions map[int64]string
}

// CompletedRoundUsageAuthorities binds a requested terminal to its active
// branch and admitted input. It cannot absorb future rounds or abandoned work.
func (r *Repository) CompletedRoundUsageAuthorities(ctx context.Context, streamUID, ownerID string, attempts []int64) (map[int64]CompletedRoundUsageAuthority, error) {
	result := map[int64]CompletedRoundUsageAuthority{}
	if len(attempts) == 0 {
		return result, nil
	}
	if len(attempts) > 2000 {
		return nil, ErrProjectionResourceLimit
	}
	for _, attempt := range attempts {
		if attempt <= 0 {
			return nil, ErrEventConflict
		}
	}
	if _, err := r.GetStream(ctx, streamUID, ownerID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(attempts)
	if err != nil {
		return nil, err
	}
	rows, err := r.readDatabase().QueryContext(ctx, `
 WITH requested AS (SELECT DISTINCT value AS attempt FROM json_each(?)), terminals AS (
  SELECT target.attempt,target.claimed_input_revision,target.finished_at,terminal.ordinal,state.active_branch_id
  FROM requested JOIN transcript_runner_attempts target ON target.stream_uid=? AND target.attempt=requested.attempt AND target.status='completed'
  JOIN transcript_branch_state state ON state.stream_uid=target.stream_uid
  JOIN transcript_branch_events terminal ON terminal.stream_uid=target.stream_uid AND terminal.branch_id=state.active_branch_id AND terminal.event_id=target.finished_event_id
 )
 SELECT target.attempt,target.claimed_input_revision,target.finished_at,origin.attempt,origin.claimed_at,origin.finished_at
 FROM terminals target
 JOIN transcript_runner_attempts origin ON origin.stream_uid=? AND origin.claimed_input_revision=target.claimed_input_revision AND origin.attempt<=target.attempt
 JOIN transcript_branch_events member ON member.stream_uid=origin.stream_uid AND member.branch_id=target.active_branch_id AND member.event_id=origin.finished_event_id AND member.ordinal<=target.ordinal
 ORDER BY target.attempt,origin.attempt`, string(raw), streamUID, streamUID)
	if err != nil {
		return nil, schemaError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var target, revision, origin int64
		var completed, started, finished time.Time
		if err := rows.Scan(&target, &revision, &completed, &origin, &started, &finished); err != nil {
			return nil, schemaError(err)
		}
		if finished.Before(started) || finished.After(completed) {
			return nil, ErrEventConflict
		}
		current := result[target]
		duration := finished.Sub(started)
		if duration < 0 || current.Elapsed > time.Duration(1<<63-1)-duration {
			return nil, ErrEventConflict
		}
		current.Attempt, current.InputRevision, current.CompletedAt = target, revision, completed.UTC()
		current.Elapsed += duration
		current.Attempts = append(current.Attempts, origin)
		result[target] = current
	}
	if err := rows.Err(); err != nil {
		return nil, schemaError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, schemaError(err)
	}
	selected := map[int64]bool{}
	for _, round := range result {
		for _, attempt := range round.Attempts {
			selected[attempt] = true
		}
	}
	origins := make([]int64, 0, len(selected))
	for attempt := range selected {
		origins = append(origins, attempt)
	}
	sessions, err := r.roundUsageAuditSessions(ctx, streamUID, ownerID, origins)
	if err != nil {
		return nil, err
	}
	for target, round := range result {
		round.AuditSessions = sessions
		result[target] = round
	}
	return result, nil
}
