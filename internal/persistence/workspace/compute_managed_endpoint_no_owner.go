package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"x-science/internal/compute"
)

func managedEndpointNoOwnerCreatedTx(ctx context.Context, tx *sql.Tx, instance compute.ManagedEndpointOwnerInstance, ref compute.ManagedEndpointActionRef, action compute.ManagedEndpointAction) (bool, error) {
	if ref.Kind != "stop" || action.Accepted || instance.InitialAction.Kind != "start" || ref.Generation <= instance.InitialAction.Generation || instance.Registration.Owner.Adopt != nil || instance.Registration.Owner.LegacyApproval != nil || instance.Identity != (compute.ManagedEndpointRuntimeIdentity{}) || instance.WorkerIdentity != (compute.ManagedEndpointRuntimeIdentity{}) {
		return false, nil
	}
	initial, err := readManagedEndpointActionTx(ctx, tx, instance.InitialAction)
	if err != nil {
		return false, err
	}
	if initial.Accepted || !initial.CancelRequested {
		return false, nil
	}
	var accepted int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM compute_managed_endpoint_actions WHERE instance_id=? AND accepted=1`, instance.ID).Scan(&accepted); err != nil {
		return false, err
	}
	return accepted == 0, nil
}

func (s *Store) ReserveManagedEndpointNoOwnerCleanup(ctx context.Context, ref compute.ManagedEndpointActionRef) (bool, error) {
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
	eligible, err := managedEndpointNoOwnerCreatedTx(ctx, tx, instance, ref, action)
	if err != nil || !eligible {
		return false, err
	}
	if instance.CleanupRef != nil && *instance.CleanupRef != instance.InitialAction {
		return false, nil
	}
	instance.CleanupRef = &instance.InitialAction
	encoded, err := json.Marshal(instance)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=? WHERE instance_id=?`, string(encoded), instance.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func validateManagedEndpointNoOwnerReceipt(instance compute.ManagedEndpointOwnerInstance, receipt compute.ManagedEndpointOwnerReceipt) error {
	if !receipt.NoScript || !receipt.Stopped || receipt.Accepted || receipt.ProcessExited || receipt.ExitCode != nil || receipt.NotStarted || receipt.OwnerVerified || receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) || receipt.StoppedProof != nil || receipt.WorkerStoppedProof == nil {
		return errors.New("no-owner receipt contradicts execution or physical-owner facts")
	}
	probe := instance
	probe.Registration.Owner = &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
	probe.Identity = receipt.WorkerStoppedProof.Identity
	return receipt.WorkerStoppedProof.ValidateFor(probe)
}
