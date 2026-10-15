package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"x-science/internal/compute"
)

func (s *Store) FinishManagedEndpointAction(ctx context.Context, ref compute.ManagedEndpointActionRef, receipt compute.ManagedEndpointOwnerReceipt) (string, error) {
	if receipt.Ref != ref || len(receipt.Transcript) > 64<<10 {
		return "", compute.ErrManagedEndpointStaleAction
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=?`, ref.InstanceID)
	if err != nil {
		return "", err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return "", err
	}
	if action.Receipt != nil {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT result_state FROM compute_managed_endpoint_actions WHERE instance_id=? AND kind=? AND claim_generation=?`, ref.InstanceID, ref.Kind, ref.Generation).Scan(&state); err != nil {
			return "", err
		}
		return state, tx.Commit()
	}
	endpoint, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance)
	if err != nil {
		return "", err
	}
	if receipt.NotStarted {
		if ref.Kind != "start" || action.Accepted || receipt.Accepted || receipt.ProcessExited || receipt.ExitCode != nil || receipt.NoScript || receipt.OwnerVerified || receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
			return "", errors.New("endpoint not-started proof contradicts admission or physical evidence")
		}
	} else if receipt.Accepted && !action.Accepted {
		return "", errors.New("endpoint receipt has no matching atomic execution admission")
	}
	if receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
		if err := receipt.Identity.ValidateFor(instance); err != nil {
			return "", err
		}
	}
	if receipt.StoppedProof != nil {
		if err := receipt.StoppedProof.ValidateFor(instance); err != nil {
			return "", err
		}
		if receipt.Identity != receipt.StoppedProof.Identity {
			return "", errors.New("stopped proof belongs to a different endpoint incarnation")
		}
	}
	validationErr := receipt.ValidateFor(instance, ref)
	if action.CancelRequested && validationErr == nil {
		return "", errors.New("cancelled endpoint action cannot commit success")
	}
	state := "live"
	if ref.Kind == "stop" {
		state = "stopped"
	}
	var failure any
	if validationErr != nil {
		state, failure = "failed", validationErr.Error()
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_actions SET accepted=(accepted OR ?),receipt_json=?,result_state=?,finished_at=? WHERE instance_id=? AND kind=? AND claim_generation=? AND receipt_json IS NULL`, receipt.Accepted, string(encoded), state, now, ref.InstanceID, ref.Kind, ref.Generation); err != nil {
		return "", err
	}
	// Failure/absence never erases the physical identity required for retrying
	// Stop. A positive first observation may establish it; never replace one.
	if receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) && receipt.OwnerVerified {
		instance.Identity = receipt.Identity
	}
	instanceRaw, err := json.Marshal(instance)
	if err != nil {
		return "", err
	}
	var retired any
	if receipt.HasPositiveStoppedProof(instance) {
		retired = now
	}
	if receipt.NotStarted && instance.Identity == (compute.ManagedEndpointRuntimeIdentity{}) && instance.Registration.Owner.Adopt == nil {
		retired = now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=?,retired_at=? WHERE instance_id=?`, string(instanceRaw), retired, instance.ID); err != nil {
		return "", err
	}
	_, token, generation, _ := parseManagedEndpointClaimHandle(ref.ClaimHandle)
	result, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoints SET state=?,last_error=?,transcript=?,state_changed_at=?,stop_claim_token='' WHERE name=? AND COALESCE(owner_user_id,'')=? AND stop_claim_token=? AND stop_claim_generation=? AND approved_script_hash=?`, state, failure, nullString(receipt.Transcript), now, instance.Endpoint, instance.UserID, token, generation, instance.ApprovedHash)
	if err != nil {
		return "", err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return "", compute.ErrManagedEndpointStaleAction
	}
	endpoint.State, endpoint.StateChangedAt, endpoint.ClaimToken = state, &now, ""
	if validationErr != nil {
		message := validationErr.Error()
		endpoint.LastError = &message
	}
	if receipt.Transcript != "" {
		if err := s.enqueueManagedEndpointTx(ctx, tx, "managed_endpoint_transcript", ref.Kind+"_finished", endpoint, receipt.Transcript); err != nil {
			return "", err
		}
	}
	if err := s.enqueueManagedEndpointTx(ctx, tx, "managed_endpoint_update", ref.Kind+"_finished", endpoint, ""); err != nil {
		return "", err
	}
	return state, tx.Commit()
}

func (s *Store) PendingManagedEndpointActions(ctx context.Context, after *compute.ManagedEndpointActionRef, limit int) ([]compute.ManagedEndpointActionRef, error) {
	if limit < 1 || limit > 128 {
		return nil, errors.New("managed endpoint action page limit must be 1..128")
	}
	query := `SELECT instance_id,kind,claim_generation,claim_handle FROM compute_managed_endpoint_actions a WHERE finished_at IS NULL AND EXISTS (SELECT 1 FROM compute_managed_endpoint_instances i WHERE i.instance_id=a.instance_id AND i.retired_at IS NULL)`
	args := []any{}
	if after != nil {
		query += ` AND (instance_id,kind,claim_generation)>(?,?,?)`
		args = append(args, after.InstanceID, after.Kind, after.Generation)
	}
	query += ` ORDER BY instance_id,kind,claim_generation LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []compute.ManagedEndpointActionRef
	for rows.Next() {
		var ref compute.ManagedEndpointActionRef
		if err := rows.Scan(&ref.InstanceID, &ref.Kind, &ref.Generation, &ref.ClaimHandle); err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, rows.Err()
}
