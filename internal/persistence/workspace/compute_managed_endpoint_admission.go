package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"x-science/internal/compute"
)

func (s *Store) AcceptManagedEndpointAction(ctx context.Context, ref compute.ManagedEndpointActionRef, worker compute.ManagedEndpointRuntimeIdentity) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, ref.InstanceID)
	if err != nil {
		return false, err
	}
	if _, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance); err != nil {
		return false, err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return false, err
	}
	if action.CancelRequested || action.Receipt != nil {
		return false, compute.ErrManagedEndpointStaleAction
	}
	if action.Accepted {
		return false, tx.Commit()
	}
	if worker.Kind != "native-unit" || worker.Unit != compute.ManagedEndpointUnitName(instance.ID) {
		return false, compute.ErrManagedEndpointOwnershipUnknown
	}
	probe := instance
	probe.Registration.Owner = &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
	probe.Identity = compute.ManagedEndpointRuntimeIdentity{}
	if err := worker.ValidateFor(probe); err != nil {
		return false, err
	}
	if instance.Identity.Kind == "native-unit" && worker != instance.Identity {
		return false, compute.ErrManagedEndpointOwnershipUnknown
	}
	if instance.WorkerIdentity != (compute.ManagedEndpointRuntimeIdentity{}) && instance.WorkerIdentity != worker && ref.Kind != "stop" {
		return false, compute.ErrManagedEndpointOwnershipUnknown
	}
	instance.WorkerIdentity = worker
	if instance.Registration.Owner.Kind == "native-unit" {
		instance.Identity = worker
	}
	encoded, err := json.Marshal(instance)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=? WHERE instance_id=? AND retired_at IS NULL`, string(encoded), instance.ID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_actions SET accepted=1,observation_error=NULL WHERE instance_id=? AND kind=? AND claim_generation=? AND claim_handle=? AND accepted=0 AND cancel_requested=0 AND receipt_json IS NULL`, ref.InstanceID, ref.Kind, ref.Generation, ref.ClaimHandle)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, compute.ErrManagedEndpointStaleAction
	}
	return true, tx.Commit()
}

func (s *Store) RecordManagedEndpointObservationFailure(ctx context.Context, ref compute.ManagedEndpointActionRef, cause error) error {
	if cause == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, ref.InstanceID)
	if err != nil {
		return err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return err
	}
	if action.Receipt != nil {
		return tx.Commit()
	}
	endpoint, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance)
	if err != nil {
		return err
	}
	message := cause.Error()
	if len(message) > 2048 {
		message = message[:2048]
	}
	if action.ObservationFailure == message {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_actions SET observation_error=? WHERE instance_id=? AND kind=? AND claim_generation=? AND receipt_json IS NULL`, message, ref.InstanceID, ref.Kind, ref.Generation); err != nil {
		return err
	}
	_, token, generation, _ := parseManagedEndpointClaimHandle(ref.ClaimHandle)
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoints SET last_error=? WHERE name=? AND stop_claim_token=? AND stop_claim_generation=?`, message, instance.Endpoint, token, generation); err != nil {
		return err
	}
	endpoint.LastError = &message
	if err := s.enqueueManagedEndpointTx(ctx, tx, "managed_endpoint_update", "owner_observation_unavailable", endpoint, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// Cancel races the very same transaction predicate as Finish. A late caller
// cancellation cannot revoke an already committed live owner.
func (s *Store) CancelManagedEndpointAction(ctx context.Context, ref compute.ManagedEndpointActionRef) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, ref.InstanceID)
	if err != nil {
		return false, err
	}
	won, err := s.cancelManagedEndpointActionTx(ctx, tx, ref, instance)
	if err != nil {
		return false, err
	}
	return won, tx.Commit()
}

func (s *Store) cancelManagedEndpointActionTx(ctx context.Context, tx *sql.Tx, ref compute.ManagedEndpointActionRef, instance compute.ManagedEndpointOwnerInstance) (bool, error) {
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return false, err
	}
	if action.Receipt != nil {
		return false, nil
	}
	if _, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_actions SET cancel_requested=1 WHERE instance_id=? AND kind=? AND claim_generation=? AND claim_handle=? AND receipt_json IS NULL`, ref.InstanceID, ref.Kind, ref.Generation, ref.ClaimHandle)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
