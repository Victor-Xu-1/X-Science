package transcript

import (
	"context"
	"encoding/json"
)

type ClaimedMemoryContext struct {
	Canonical bool
	Mode      string
	Explicit  bool
}

// Only the claimed Memory projection folds explicit composer input. General
// runtime configuration continues to read its original runtimeConfig patches.
func claimedMemoryPatch(payload map[string]json.RawMessage) (string, bool, error) {
	for _, key := range []string{"inputData", "runtimeConfig"} {
		raw, exists := payload[key]
		if !exists {
			continue
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil || key == "runtimeConfig" && data == nil {
			return "", false, ErrEventConflict
		}
		value, explicit := data["memory_mode"]
		if !explicit {
			value, explicit = data["memoryMode"]
		}
		if !explicit {
			continue
		}
		mode, valid := value.(string)
		if !valid || mode != "on" && mode != "off" {
			return "", false, ErrEventConflict
		}
		return mode, true, nil
	}
	return "", false, nil
}

// ClaimedRunnerMemoryContext resolves only Memory from configuration already
// admitted to this live claim. The same read snapshot validates claim, owner,
// active branch and publication cutoff; newer queued input is never permission.
// Absence remains unknown rather than inheriting a mutable global default.
func (r *Repository) ClaimedRunnerMemoryContext(ctx context.Context, claim RunnerClaim) (ClaimedMemoryContext, error) {
	var result ClaimedMemoryContext
	if r == nil || r.readDatabase() == nil {
		return result, ErrSchemaUnavailable
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
	authority, found, err := getFrameAuthorityBySessionQuery(ctx, conn, stream.OwnerID, stream.SessionID)
	if err != nil {
		return result, err
	}
	if !found || authority.OwnerID != stream.OwnerID || authority.SessionID != stream.SessionID || authority.ActiveStreamUID != stream.UID || authority.ActiveEpoch != stream.Epoch {
		return result, ErrEventConflict
	}
	if !authority.TranscriptPayloadActive() {
		// The existing authority reader accepts only a real legacy pair or valid
		// payload authority. Errors and contradictory metadata are not legacy.
		return result, nil
	}
	result.Canonical = true
	// Validate the admission/branch provenance with the existing claimed-input
	// reader, including copied-prefix and branch-generation rules.
	input, err := readAdmittedRunnerInputContextConn(ctx, conn, stream, claim.ClaimedInputRevision, false)
	if err != nil {
		return result, err
	}
	if input == nil {
		return result, nil
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
		return result, schemaError(err)
	}
	config, found, err := readFrameRuntimeConfig(ctx, conn, stream, &selection, &claim.ClaimedInputRevision)
	if err != nil || !found {
		return result, err
	}
	value, found := config["memory_mode"]
	if !found {
		value, found = config["memoryMode"]
	}
	if !found {
		return result, nil
	}
	mode, valid := value.(string)
	if !valid || mode != "on" && mode != "off" {
		return result, ErrEventConflict
	}
	result.Mode, result.Explicit = mode, true
	return result, nil
}
