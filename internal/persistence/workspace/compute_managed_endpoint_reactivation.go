package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"x-science/internal/compute"
)

// A parked container remains the same physical owner. Reopening it requires a
// fresh start claim and acceptance, not a rewrite or replay of its old action.
func managedEndpointRetiredOwnerTx(ctx context.Context, tx *sql.Tx, name, user, hash string, warmStart bool) (compute.ManagedEndpointOwnerInstance, error) {
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `endpoint_name=? AND owner_user_id=? AND approved_hash=? AND retired_at IS NOT NULL ORDER BY retired_at DESC LIMIT 1`, name, user, hash)
	if err != nil {
		return instance, err
	}
	if warmStart && (instance.Registration.Owner.Kind != "docker" || instance.Identity.Kind != "docker") {
		return instance, sql.ErrNoRows
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT receipt_json FROM compute_managed_endpoint_actions WHERE instance_id=? AND receipt_json IS NOT NULL ORDER BY claim_generation DESC LIMIT 1`, instance.ID).Scan(&raw); err != nil {
		return instance, err
	}
	var receipt compute.ManagedEndpointOwnerReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return instance, compute.ErrManagedEndpointCorruptOwner
	}
	if !receipt.HasPositiveStoppedProof(instance) {
		return instance, errors.New("parked Docker endpoint has no positive original-owner stop proof")
	}
	if warmStart && (receipt.StoppedProof.State != "exited" && receipt.StoppedProof.State != "dead") {
		return instance, sql.ErrNoRows
	}
	if warmStart {
		instance.WorkerIdentity = compute.ManagedEndpointRuntimeIdentity{}
		instance.CleanupRef = nil
	}
	encoded, err := json.Marshal(instance)
	if err != nil {
		return instance, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=?,retired_at=NULL WHERE instance_id=? AND retired_at IS NOT NULL`, string(encoded), instance.ID); err != nil {
		return instance, err
	}
	return instance, nil
}
