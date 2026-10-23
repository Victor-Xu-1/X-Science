package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ClaimedToolDiscovery is read-only context provenance, never tool permission.
// The input and latest terminal checkpoint share the live claim's read snapshot.
// An absent or failed checkpoint is unknown, not a reason to reuse an older one.
type ClaimedToolDiscovery struct {
	Input               *ClaimedInputContext
	EventID             int64
	PublicationSequence int64
	PayloadJSON         json.RawMessage
}

// ReadClaimedToolDiscovery reads the latest named discovery on the active branch,
// independently of provider replay/compaction limits. Original shared-prefix
// receipts remain historical facts; queued inputs cannot replace the claim's
// admitted ordinary input. The caller validates the tool's result contract.
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
	if err != nil || result.Input == nil {
		return result, err
	}
	if result.Input.Association != nil {
		result.Input.Association.RunnerAttempt = claim.Attempt
	}
	err = conn.QueryRowContext(ctx, `SELECT event.event_id,event.publication_seq,event.payload_json
		FROM transcript_branch_state state
		JOIN transcript_branch_events member ON member.stream_uid=state.stream_uid AND member.branch_id=state.active_branch_id
		JOIN transcript_events event ON event.stream_uid=member.stream_uid AND event.event_id=member.event_id
		JOIN transcript_runner_attempts attempt ON attempt.stream_uid=event.stream_uid AND attempt.attempt=event.runner_attempt
		WHERE state.stream_uid=? AND attempt.claimed_input_revision<=?
			AND event.event_type='runner_checkpoint' AND event.source='payload'
			AND json_extract(event.payload_json,'$.toolName')=?
			AND json_extract(event.payload_json,'$.toolPhase') IN ('completed','failed')
		ORDER BY event.publication_seq DESC LIMIT 1`, stream.UID, claim.ClaimedInputRevision, toolName).
		Scan(&result.EventID, &result.PublicationSequence, &result.PayloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return ClaimedToolDiscovery{}, schemaError(err)
	}
	if len(result.PayloadJSON) == 0 || len(result.PayloadJSON) > maxEventPayloadBytes {
		return ClaimedToolDiscovery{}, ErrEventConflict
	}
	return result, nil
}
