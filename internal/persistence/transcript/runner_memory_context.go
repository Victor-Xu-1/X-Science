package transcript

import (
	"context"
)

// ClaimedRunnerMemoryMode resolves only Memory from configuration already
// admitted to this live claim. The same read snapshot validates claim, owner,
// active branch and publication cutoff; newer queued input is never permission.
// Absence remains unknown rather than inheriting a mutable global default.
func (r *Repository) ClaimedRunnerMemoryMode(ctx context.Context, claim RunnerClaim) (string, bool, error) {
	if r == nil || r.readDatabase() == nil {
		return "", false, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", false, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stream, err := validateClaimConn(ctx, conn, claim, r.now().UTC(), true)
	if err != nil {
		return "", false, schemaError(err)
	}
	// Validate the admission/branch provenance with the existing claimed-input
	// reader, including copied-prefix and branch-generation rules.
	input, err := readAdmittedRunnerInputContextConn(ctx, conn, stream, claim.ClaimedInputRevision, false)
	if err != nil {
		return "", false, err
	}
	if input == nil {
		return "", false, nil
	}
	var selection AsideHistorySelection
	if err := conn.QueryRowContext(ctx, `SELECT state.active_branch_id,state.generation,
		COALESCE(MAX(event.publication_seq),0)
		FROM transcript_branch_state state
		LEFT JOIN transcript_input_admissions admission ON admission.stream_uid=state.stream_uid
			AND admission.input_revision<=? AND admission.source_kind='event'
		LEFT JOIN transcript_events event ON event.stream_uid=admission.stream_uid AND event.event_id=admission.event_id
			AND EXISTS(SELECT 1 FROM transcript_branch_events member WHERE member.stream_uid=state.stream_uid
				AND member.branch_id=state.active_branch_id AND member.event_id=event.event_id)
		WHERE state.stream_uid=? GROUP BY state.active_branch_id,state.generation`, claim.ClaimedInputRevision, stream.UID).
		Scan(&selection.BranchID, &selection.Generation, &selection.ThroughPublication); err != nil {
		return "", false, schemaError(err)
	}
	config, found, err := readFrameRuntimeConfig(ctx, conn, stream, &selection, &claim.ClaimedInputRevision)
	if err != nil || !found {
		return "", false, err
	}
	value, found := config["memory_mode"]
	if !found {
		value, found = config["memoryMode"]
	}
	if !found {
		return "", false, nil
	}
	mode, valid := value.(string)
	if !valid || mode != "on" && mode != "off" {
		return "", false, ErrEventConflict
	}
	return mode, true, nil
}
