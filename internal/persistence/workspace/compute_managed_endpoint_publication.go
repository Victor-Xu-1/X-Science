package workspace

import (
	"context"
	"database/sql"
	"errors"

	"x-science/internal/compute"
)

// WithManagedEndpointBootstrapPublication orders the sole private CLI writer
// before cancellation/retirement/Delete on this database, across Store handles.
// Evidence readers intentionally do not grant this authority. No execution is
// accepted here, and the callback must be synchronous, small, and filesystem-only.
func (s *Store) WithManagedEndpointBootstrapPublication(ctx context.Context, input compute.ManagedEndpointBootstrap, worker compute.ManagedEndpointRuntimeIdentity, publish func(compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction) error) error {
	if ctx == nil || publish == nil {
		return errors.New("managed endpoint bootstrap publisher is unavailable")
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// database/sql otherwise rolls back BeginTx(callerCtx) asynchronously on
	// cancellation, releasing SQLite's fence while the FS callback still runs.
	// Only this stack may end the transaction, after the callback has returned.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A deferred read transaction is not a cross-process publication fence.
	// Obtain SQLite's writer lock before reading any authorization evidence.
	locked, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=instance_json WHERE instance_id=? AND retired_at IS NULL`, input.Scope.InstanceID)
	if err != nil {
		return err
	}
	count, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return compute.ErrManagedEndpointStaleAction
	}
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, input.Scope.InstanceID)
	if err != nil {
		return err
	}
	if !input.Matches(instance) {
		return compute.ErrManagedEndpointCorruptOwner
	}
	action, err := readManagedEndpointActionTx(ctx, tx, input.RequestedAction)
	if err != nil {
		return err
	}
	if err := validateManagedEndpointBootstrapWorker(instance, input.RequestedAction, worker); err != nil {
		return err
	}
	if err := s.authorizeManagedEndpointPublicationTx(ctx, tx, instance, action, worker); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if err := publish(instance, action); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return tx.Commit()
}

func validateManagedEndpointBootstrapWorker(instance compute.ManagedEndpointOwnerInstance, ref compute.ManagedEndpointActionRef, worker compute.ManagedEndpointRuntimeIdentity) error {
	if worker.Kind != "native-unit" || worker.Unit != compute.ManagedEndpointUnitName(instance.ID) {
		return compute.ErrManagedEndpointOwnershipUnknown
	}
	probe := instance
	probe.Registration.Owner = &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
	probe.Identity = compute.ManagedEndpointRuntimeIdentity{}
	if err := worker.ValidateFor(probe); err != nil {
		return err
	}
	if instance.Identity.Kind == "native-unit" && instance.Identity != worker {
		return compute.ErrManagedEndpointOwnershipUnknown
	}
	if instance.WorkerIdentity != (compute.ManagedEndpointRuntimeIdentity{}) && instance.WorkerIdentity != worker && ref.Kind != "stop" {
		return compute.ErrManagedEndpointOwnershipUnknown
	}
	return nil
}

func (s *Store) authorizeManagedEndpointPublicationTx(ctx context.Context, tx *sql.Tx, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, worker compute.ManagedEndpointRuntimeIdentity) error {
	if action.Receipt != nil {
		// Re-observation of the same accepted live worker is independent of the
		// web process. It never authorizes a replacement or replays the script.
		if action.Ref.Kind != "start" || !action.Accepted || action.CancelRequested || instance.CleanupRef != nil || instance.WorkerIdentity != worker || action.Receipt.ValidateFor(instance, action.Ref) != nil {
			return compute.ErrManagedEndpointStaleAction
		}
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM compute_managed_endpoints WHERE name=? AND owner_user_id=? AND approved_script_hash=? AND state='live' AND stop_claim_generation=? AND COALESCE(stop_claim_token,'')=''`, instance.Endpoint, instance.UserID, instance.ApprovedHash, action.Ref.Generation).Scan(&current); err != nil {
			return err
		}
		if current != 1 {
			return compute.ErrManagedEndpointStaleAction
		}
		return nil
	}
	if _, err := s.managedEndpointActionClaimTx(ctx, tx, action.Ref, instance); err != nil {
		return err
	}
	noOwner, err := managedEndpointNoOwnerCreatedTx(ctx, tx, instance, action.Ref, action)
	if err != nil {
		return err
	}
	if noOwner {
		// The current Stop's existing reservation can launch only its idle
		// observer, even for cancelled Stop. It must not accept either script.
		if instance.CleanupRef == nil || *instance.CleanupRef != instance.InitialAction {
			return compute.ErrManagedEndpointStaleAction
		}
		return nil
	}
	if action.CancelRequested {
		return compute.ErrManagedEndpointStaleAction
	}
	if instance.CleanupRef != nil && *instance.CleanupRef != action.Ref {
		return compute.ErrManagedEndpointActionPending
	}
	if action.Accepted && instance.WorkerIdentity != worker {
		return compute.ErrManagedEndpointOwnershipUnknown
	}
	return nil
}
