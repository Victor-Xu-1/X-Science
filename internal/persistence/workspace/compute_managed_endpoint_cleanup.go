package workspace

import (
	"context"
	"encoding/json"
	"errors"

	"x-science/internal/compute"
)

// The short transaction reserves physical cleanup, not an OS process lifetime.
// Once reserved, a new action cannot be admitted until positive completion;
// once a newer action was admitted, an old cancellation cannot reserve cleanup.
func (s *Store) AcquireManagedEndpointCleanup(ctx context.Context, ref compute.ManagedEndpointActionRef, failed *compute.ManagedEndpointOwnerReceipt) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, ref.InstanceID)
	if err != nil {
		return false, err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return false, err
	}
	if action.Receipt != nil {
		return false, tx.Commit()
	}
	if instance.CleanupRef != nil {
		return *instance.CleanupRef == ref, tx.Commit()
	}
	if _, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance); err != nil {
		if errors.Is(err, compute.ErrManagedEndpointStaleAction) {
			return false, tx.Commit()
		}
		return false, err
	}
	if failed != nil {
		if failed.Ref != ref || failed.Failure == "" || !failed.Accepted || !action.Accepted || ref.Kind != "start" {
			return false, compute.ErrManagedEndpointCorruptOwner
		}
		if failed.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
			if err := failed.Identity.ValidateFor(instance); err != nil {
				return false, err
			}
		}
		if _, err := s.cancelManagedEndpointActionTx(ctx, tx, ref, instance); err != nil {
			return false, err
		}
	} else if !action.CancelRequested {
		return false, tx.Commit()
	}
	instance.CleanupRef = &ref
	encoded, err := json.Marshal(instance)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_instances SET instance_json=? WHERE instance_id=? AND retired_at IS NULL`, string(encoded), instance.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// A superseded cancelled action has no authority to change the newer logical
// claim or retire its instance. Preserve actual known facts, or explicitly say
// that no command exit was available, and close only this historical receipt.
func (s *Store) SettleSupersededManagedEndpointAction(ctx context.Context, ref compute.ManagedEndpointActionRef, known *compute.ManagedEndpointOwnerReceipt) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=?`, ref.InstanceID)
	if err != nil {
		return false, err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return false, err
	}
	if action.Receipt != nil || !action.CancelRequested {
		return false, tx.Commit()
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT stop_claim_generation FROM compute_managed_endpoints WHERE name=? AND owner_user_id=?`, instance.Endpoint, instance.UserID).Scan(&generation); err != nil {
		return false, err
	}
	if generation <= ref.Generation {
		return false, tx.Commit()
	}
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: ref, Accepted: action.Accepted, Failure: "cancelled action was superseded; no terminal command result was available at reconciliation"}
	if known != nil {
		receipt = *known
		if receipt.Ref != ref || len(receipt.Transcript) > 64<<10 || receipt.Accepted && !action.Accepted || receipt.NotStarted && action.Accepted {
			return false, compute.ErrManagedEndpointCorruptOwner
		}
		if receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
			if err := receipt.Identity.ValidateFor(instance); err != nil {
				return false, err
			}
		}
		if receipt.StoppedProof != nil {
			if err := receipt.StoppedProof.ValidateFor(instance); err != nil {
				return false, err
			}
		}
		if receipt.Failure == "" {
			receipt.Failure = "cancelled action was superseded before its logical handoff"
		}
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE compute_managed_endpoint_actions SET receipt_json=?,result_state='superseded',finished_at=? WHERE instance_id=? AND kind=? AND claim_generation=? AND claim_handle=? AND cancel_requested=1 AND receipt_json IS NULL`, string(encoded), s.now().UTC(), ref.InstanceID, ref.Kind, ref.Generation, ref.ClaimHandle)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) CurrentManagedEndpointAction(ctx context.Context, instanceID string) (compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return compute.ManagedEndpointOwnerInstance{}, compute.ManagedEndpointAction{}, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `instance_id=? AND retired_at IS NULL`, instanceID)
	if err != nil {
		return instance, compute.ManagedEndpointAction{}, err
	}
	endpoint, err := scanManagedEndpoint(tx.QueryRowContext(ctx, `SELECT `+managedEndpointColumns+` FROM compute_managed_endpoints WHERE name=? AND owner_user_id=?`, instance.Endpoint, instance.UserID))
	if err != nil {
		return instance, compute.ManagedEndpointAction{}, err
	}
	kind := "start"
	if endpoint.State == "stopping" {
		kind = "stop"
	} else if endpoint.State != "starting" {
		return instance, compute.ManagedEndpointAction{}, compute.ErrManagedEndpointStaleAction
	}
	ref := compute.ManagedEndpointActionRef{InstanceID: instance.ID, Kind: kind, ClaimHandle: endpoint.ClaimHandle, Generation: endpoint.ClaimGeneration}
	if _, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance); err != nil {
		return instance, compute.ManagedEndpointAction{}, err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return instance, action, err
	}
	return instance, action, tx.Commit()
}
