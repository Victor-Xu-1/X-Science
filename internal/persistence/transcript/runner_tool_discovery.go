package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ClaimedToolDiscovery is original read-only context evidence, not permission.
// An observed but invalid/failed terminal must not fall back to older success.
type ClaimedToolDiscovery struct {
	Input               *ClaimedInputContext
	EventID             int64
	PublicationSequence int64
	ToolCallID          string
	State               string
	ResultSHA256        string
	PayloadJSON         json.RawMessage
}

// ReadClaimedToolDiscovery keeps the live claim/input and latest named terminal
// in one read snapshot. It reuses the original canonical batch/unique-outcome
// relation, independently of provider replay and compaction limits.
func (r *Repository) ReadClaimedToolDiscovery(ctx context.Context, claim RunnerClaim, toolName string) (ClaimedToolDiscovery, error) {
	var result ClaimedToolDiscovery
	if r == nil || r.readDatabase() == nil {
		return result, ErrSchemaUnavailable
	}
	if toolName == "" || len(toolName) > 128 || strings.TrimSpace(toolName) != toolName {
		return result, ErrEventConflict
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return result, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := validateClaimConn(ctx, conn, claim, r.now().UTC(), true)
	if err != nil {
		return result, schemaError(err)
	}
	result.Input, err = readAdmittedRunnerInputContextConn(ctx, conn, stream, claim.ClaimedInputRevision, false)
	if err != nil || result.Input == nil || result.Input.Association == nil {
		return result, err
	}
	result.Input.Association.RunnerAttempt = claim.Attempt
	scope := result.Input.Association.Scope
	var eventType, source string
	err = conn.QueryRowContext(ctx, `WITH
		read_branch(stream_uid,branch_id,generation,through_publication_seq) AS (VALUES(?,?,?,?)),
		`+canonicalToolBatchReadCTE+`,
		selected(event_id) AS (
			SELECT item.terminal_event_id FROM eligible_tool_batches eligible
			JOIN transcript_tool_call_batches batch ON batch.batch_id=eligible.batch_id
			JOIN transcript_tool_call_items item ON item.batch_id=batch.batch_id AND item.stream_uid=batch.stream_uid
			JOIN transcript_events event ON event.stream_uid=item.stream_uid AND event.event_id=item.terminal_event_id
			JOIN transcript_runner_attempts attempt ON attempt.stream_uid=event.stream_uid AND attempt.attempt=event.runner_attempt
			WHERE batch.owner_user_id=? AND batch.admitted_input_revision<=? AND attempt.claimed_input_revision<=?
				AND item.tool_name=? AND item.terminal_event_id IS NOT NULL AND event.publication_seq<=?
			ORDER BY event.publication_seq DESC,item.ordinal DESC LIMIT 1
		),
		`+canonicalToolOutcomeReadCTE+`
		SELECT event.event_id,event.publication_seq,event.event_type,event.source,event.payload_json,
			COALESCE(outcome.tool_call_id,''),COALESCE(item.state,''),COALESCE(item.terminal_result_sha256,'')
		FROM selected JOIN transcript_events event ON event.stream_uid=? AND event.event_id=selected.event_id
		LEFT JOIN canonical_tool_outcomes outcome ON outcome.stream_uid=event.stream_uid AND outcome.terminal_event_id=event.event_id
		LEFT JOIN transcript_tool_call_items item ON item.batch_id=outcome.batch_id AND item.ordinal=outcome.ordinal`,
		stream.UID, scope.BranchID, scope.BranchGeneration, scope.ThroughPublicationSequence,
		stream.OwnerID, claim.ClaimedInputRevision, claim.ClaimedInputRevision, toolName, scope.ThroughPublicationSequence,
		stream.UID).Scan(&result.EventID, &result.PublicationSequence, &eventType, &source, &result.PayloadJSON,
		&result.ToolCallID, &result.State, &result.ResultSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return ClaimedToolDiscovery{}, schemaError(err)
	}
	if eventType != "runner_checkpoint" || source != string(EventSourcePayload) ||
		len(result.PayloadJSON) == 0 || len(result.PayloadJSON) > maxEventPayloadBytes {
		result.PayloadJSON, result.State, result.ResultSHA256 = nil, "", ""
	}
	return result, nil
}
