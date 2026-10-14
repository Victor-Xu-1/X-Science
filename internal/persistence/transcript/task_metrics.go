package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// RunnerTaskMetricsAuthority identifies durable metric scopes for an entire
// frame task and its latest logical input revision. Retries remain part of the
// logical revision that admitted them.
type RunnerTaskMetricsAuthority struct {
	TaskAttempts        []int64
	LatestAttempts      []int64
	TaskToolCallCount   int64
	LatestToolCallCount int64
}

// GetRunnerTaskMetricsAuthority returns task-scoped counters without deriving
// them from the bounded web transcript. The owner check is performed before
// reading either runner attempts or tool-call batches.
func (r *Repository) GetRunnerTaskMetricsAuthority(
	ctx context.Context,
	streamUID, ownerID string,
	inputRevision int64,
) (RunnerTaskMetricsAuthority, bool, error) {
	db := r.readDatabase()
	if db == nil {
		return RunnerTaskMetricsAuthority{}, false, ErrSchemaUnavailable
	}
	streamUID = strings.TrimSpace(streamUID)
	ownerID = strings.TrimSpace(ownerID)
	if streamUID == "" || ownerID == "" || inputRevision <= 0 {
		return RunnerTaskMetricsAuthority{}, false, errors.New("stream, owner, and a positive input revision are required")
	}
	if _, err := r.GetStream(ctx, streamUID, ownerID); err != nil {
		return RunnerTaskMetricsAuthority{}, false, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT attempt,claimed_input_revision FROM transcript_runner_attempts
		WHERE stream_uid=? ORDER BY attempt`, streamUID)
	if err != nil {
		return RunnerTaskMetricsAuthority{}, false, schemaError(err)
	}
	defer rows.Close()
	result := RunnerTaskMetricsAuthority{}
	for rows.Next() {
		var attempt, claimedInputRevision int64
		if err := rows.Scan(&attempt, &claimedInputRevision); err != nil {
			return RunnerTaskMetricsAuthority{}, false, schemaError(err)
		}
		if attempt <= 0 || claimedInputRevision <= 0 {
			return RunnerTaskMetricsAuthority{}, false, ErrEventConflict
		}
		result.TaskAttempts = append(result.TaskAttempts, attempt)
		if claimedInputRevision == inputRevision {
			result.LatestAttempts = append(result.LatestAttempts, attempt)
		}
	}
	if err := rows.Err(); err != nil {
		return RunnerTaskMetricsAuthority{}, false, schemaError(err)
	}
	if len(result.TaskAttempts) == 0 {
		return RunnerTaskMetricsAuthority{}, false, nil
	}

	readToolCallCount := func(query string, arguments ...any) (int64, error) {
		var callCount sql.NullInt64
		if err := db.QueryRowContext(ctx, query, arguments...).Scan(&callCount); err != nil {
			return 0, schemaError(err)
		}
		if !callCount.Valid {
			return 0, nil
		}
		if callCount.Int64 < 0 {
			return 0, ErrEventConflict
		}
		return callCount.Int64, nil
	}
	result.TaskToolCallCount, err = readToolCallCount(`
		SELECT SUM(call_count) FROM transcript_tool_call_batches
		WHERE owner_user_id=? AND stream_uid=?`, ownerID, streamUID)
	if err != nil {
		return RunnerTaskMetricsAuthority{}, false, err
	}
	// Approval responses advance an existing attempt's claimed revision, while
	// batch admission identity stays immutable. Use the same originating attempts
	// captured for model usage, even if a new approval arrives between reads.
	// Count each root batch once rather than replayed or nested tool events.
	latestAttemptsJSON, err := json.Marshal(result.LatestAttempts)
	if err != nil {
		return RunnerTaskMetricsAuthority{}, false, err
	}
	result.LatestToolCallCount, err = readToolCallCount(`
		SELECT SUM(batch.call_count) FROM transcript_tool_call_batches batch
		WHERE batch.owner_user_id=? AND batch.stream_uid=?
			AND batch.source_runner_attempt IN (SELECT value FROM json_each(?))`,
		ownerID, streamUID, string(latestAttemptsJSON),
	)
	if err != nil {
		return RunnerTaskMetricsAuthority{}, false, err
	}
	return result, true, nil
}

// GetRunnerTaskTimingSummary returns cumulative active runner time for the
// whole frame task. It deliberately excludes idle gaps between user rounds.
func (r *Repository) GetRunnerTaskTimingSummary(
	ctx context.Context,
	streamUID, ownerID string,
) (RunnerTaskTimingSummary, bool, error) {
	_, task, found, err := r.GetRunnerTimingSummaries(ctx, streamUID, ownerID)
	return task, found, err
}
