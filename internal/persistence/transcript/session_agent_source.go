package transcript

import (
	"context"
	"database/sql"

	"x-science/internal/sessionagent"
)

// AgentOperationSource identifies the durable checkpoint that originally
// admitted a recovered operation. It is not supplied through a user API.
type AgentOperationSource struct {
	StreamUID, FrameID string
	EventID, Attempt   int64
}

type RunnerAgentSource struct {
	Snapshot             *sessionagent.Snapshot
	InputData            map[string]any
	Managed, Unavailable bool
}

// ClaimedRunnerAgentOperationSource retains an old approved operation's own
// configuration/composer context when a later user input has already arrived.
// The live claim permits execution; historical records provide provenance only.
func (r *Repository) ClaimedRunnerAgentOperationSource(ctx context.Context, claim RunnerClaim, origin AgentOperationSource) (RunnerAgentSource, error) {
	var result RunnerAgentSource
	err := r.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		stream, err := validateClaimConn(ctx, tx.conn, claim, r.now().UTC(), true)
		if err != nil {
			return err
		}
		if stream.Kind != StreamKindFrameRef || origin.StreamUID != stream.UID || origin.FrameID != stream.FrameID || origin.EventID <= 0 || origin.Attempt <= 0 {
			return ErrOwnerMismatch
		}
		var sourceAttempt sql.NullInt64
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT e.runner_attempt,EXISTS(SELECT 1 FROM transcript_branch_events m JOIN transcript_branch_state b
			ON b.stream_uid=m.stream_uid AND b.active_branch_id=m.branch_id WHERE m.stream_uid=e.stream_uid AND m.event_id=e.event_id)
			FROM transcript_events e WHERE e.stream_uid=? AND e.event_id=?`, stream.UID, origin.EventID).Scan(&sourceAttempt, &member); err != nil {
			return err
		}
		if !sourceAttempt.Valid || sourceAttempt.Int64 != origin.Attempt || !member {
			return ErrEventConflict
		}
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT claimed_input_revision FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`, stream.UID, origin.Attempt).Scan(&revision); err != nil {
			return err
		}
		input, err := readAdmittedRunnerInputContextConn(ctx, tx.conn, stream, revision, true)
		if err != nil {
			return err
		}
		if input != nil {
			result.InputData = input.InputData
		}
		established, err := sessionagent.Established(ctx, tx, stream.FrameID)
		if err != nil || !established {
			return err
		}
		result.Managed = true
		scope, err := sessionagent.ResolveScope(ctx, tx, stream.OwnerID, stream.FrameID)
		if err != nil {
			return err
		}
		binding, err := sessionagent.ReadClaim(ctx, tx, stream.OwnerID, scope, stream.UID, stream.Epoch, origin.Attempt)
		if err != nil {
			return err
		}
		if binding == nil {
			// Pre-feature work has no historical immutable definition. The first
			// explicit selection freezes its previous identity as a compatibility
			// fallback; never substitute a later desired/applied specialist.
			head, _, _, err := sessionagent.ReadHead(ctx, tx, stream.OwnerID, scope)
			if err != nil {
				return err
			}
			if head.Fallback.SnapshotID == "" {
				result.Unavailable = true
				return nil
			}
			definition, err := sessionagent.ReadSnapshot(ctx, tx, stream.OwnerID, scope, head.Fallback.SnapshotID)
			if err != nil {
				return err
			}
			result.Snapshot = &definition
			return nil
		}
		definition, err := sessionagent.ReadSnapshot(ctx, tx, stream.OwnerID, scope, binding.Selection.SnapshotID)
		if err != nil {
			return err
		}
		result.Snapshot = &definition
		return nil
	})
	return result, err
}
