package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"x-science/internal/compute"
)

func managedEndpointDatabasePathTx(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return "", err
		}
		if name == "main" {
			if !filepath.IsAbs(path) {
				return "", errors.New("managed endpoint ownership requires a file-backed workspace")
			}
			return path, nil
		}
	}
	return "", errors.New("managed endpoint workspace database identity is missing")
}

func readManagedEndpointInstanceTx(ctx context.Context, tx *sql.Tx, clause string, args ...any) (compute.ManagedEndpointOwnerInstance, error) {
	var instance compute.ManagedEndpointOwnerInstance
	var id, user, name, hash, raw string
	var generation int64
	var serviceDir sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT instance_id,owner_user_id,endpoint_name,approved_hash,start_generation,instance_json,(SELECT e.service_dir FROM compute_managed_endpoints e WHERE e.name=i.endpoint_name AND COALESCE(e.owner_user_id,'')=i.owner_user_id) FROM compute_managed_endpoint_instances i WHERE `+clause, args...).Scan(&id, &user, &name, &hash, &generation, &raw, &serviceDir); err != nil {
		return instance, err
	}
	if err := json.Unmarshal([]byte(raw), &instance); err != nil {
		return instance, fmt.Errorf("%w: malformed instance JSON", compute.ErrManagedEndpointCorruptOwner)
	}
	if instance.ID != id || instance.UserID != user || instance.Endpoint != name || instance.ApprovedHash != hash || instance.StartGeneration != generation || !serviceDir.Valid || instance.ServiceDir != serviceDir.String {
		return instance, fmt.Errorf("%w: SQL and instance keys disagree", compute.ErrManagedEndpointCorruptOwner)
	}
	path, err := managedEndpointDatabasePathTx(ctx, tx)
	if err != nil {
		return instance, err
	}
	if instance.WorkspacePath != path {
		return instance, fmt.Errorf("%w: workspace path disagrees", compute.ErrManagedEndpointCorruptOwner)
	}
	if err := instance.Validate(); err != nil {
		return instance, fmt.Errorf("%w: %v", compute.ErrManagedEndpointCorruptOwner, err)
	}
	var initialHandle string
	if err := tx.QueryRowContext(ctx, `SELECT claim_handle FROM compute_managed_endpoint_actions WHERE instance_id=? AND kind=? AND claim_generation=?`, instance.ID, instance.InitialAction.Kind, instance.StartGeneration).Scan(&initialHandle); err != nil || initialHandle != instance.InitialAction.ClaimHandle {
		return instance, fmt.Errorf("%w: original action and instance disagree", compute.ErrManagedEndpointCorruptOwner)
	}
	if instance.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
		if err := instance.Identity.ValidateFor(instance); err != nil {
			return instance, fmt.Errorf("%w: %v", compute.ErrManagedEndpointCorruptOwner, err)
		}
	}
	return instance, nil
}

func (s *Store) managedEndpointActionClaimTx(ctx context.Context, tx *sql.Tx, ref compute.ManagedEndpointActionRef, instance compute.ManagedEndpointOwnerInstance) (ManagedEndpoint, error) {
	name, token, generation, ok := parseManagedEndpointClaimHandle(ref.ClaimHandle)
	if !ok || name != instance.Endpoint || generation != ref.Generation {
		return ManagedEndpoint{}, compute.ErrManagedEndpointStaleAction
	}
	state := "starting"
	if ref.Kind == "stop" {
		state = "stopping"
	}
	endpoint, err := scanManagedEndpoint(tx.QueryRowContext(ctx, `SELECT `+managedEndpointColumns+` FROM compute_managed_endpoints WHERE name=? AND COALESCE(owner_user_id,'')=? AND state=? AND stop_claim_token=? AND stop_claim_generation=? AND approved_script_hash=?`, name, instance.UserID, state, token, generation, instance.ApprovedHash))
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedEndpoint{}, compute.ErrManagedEndpointStaleAction
	}
	return endpoint, err
}

func (s *Store) PrepareManagedEndpointAction(ctx context.Context, claim compute.ManagedEndpointClaim, instanceID, runtimeRoot string) (compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction, error) {
	var instance compute.ManagedEndpointOwnerInstance
	var action compute.ManagedEndpointAction
	if err := claim.Registration.Owner.Validate(); err != nil {
		return instance, action, err
	}
	name, token, generation, ok := parseManagedEndpointClaimHandle(claim.Handle)
	if !ok || name != claim.Name || generation != claim.Generation || claim.UserID == "" || compute.ApprovedManagedEndpointHash(claim.Registration) != strings.TrimPrefix(claim.ApprovedHash, "sha256:") {
		return instance, action, compute.ErrManagedEndpointStaleAction
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return instance, action, err
	}
	defer tx.Rollback()
	endpoint, err := scanManagedEndpoint(tx.QueryRowContext(ctx, `SELECT `+managedEndpointColumns+` FROM compute_managed_endpoints WHERE name=? AND COALESCE(owner_user_id,'')=? AND stop_claim_token=? AND stop_claim_generation=? AND approved_script_hash=?`, name, claim.UserID, token, generation, claim.ApprovedHash))
	if err != nil {
		return instance, action, err
	}
	if claim.ServiceDir != endpoint.ServiceDir {
		return instance, action, compute.ErrManagedEndpointStaleAction
	}
	kind := "start"
	if endpoint.State == "stopping" {
		kind = "stop"
	} else if endpoint.State != "starting" {
		return instance, action, compute.ErrManagedEndpointStaleAction
	}
	instance, err = readManagedEndpointInstanceTx(ctx, tx, `endpoint_name=? AND owner_user_id=? AND retired_at IS NULL`, name, claim.UserID)
	if err == nil {
		if instance.ApprovedHash != claim.ApprovedHash || (kind == "start" && instance.StartGeneration != generation) {
			return instance, action, compute.ErrManagedEndpointOwnershipUnknown
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if kind == "stop" || claim.Registration.Owner.Kind == "docker" {
			instance, err = managedEndpointRetiredOwnerTx(ctx, tx, name, claim.UserID, claim.ApprovedHash, kind == "start")
			if err == nil {
				goto preparedOwner
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return instance, action, err
			}
		}
		if kind == "stop" && claim.Registration.Owner.Adopt == nil {
			return instance, action, compute.ErrManagedEndpointOwnershipUnknown
		}
		path, err := managedEndpointDatabasePathTx(ctx, tx)
		if err != nil {
			return instance, action, err
		}
		instance = compute.ManagedEndpointOwnerInstance{ID: instanceID, UserID: claim.UserID, Endpoint: name, Registration: claim.Registration, ApprovedHash: claim.ApprovedHash, StartGeneration: generation, RuntimeRoot: runtimeRoot, ServiceDir: endpoint.ServiceDir, WorkspacePath: path, InitialAction: compute.ManagedEndpointActionRef{InstanceID: instanceID, Kind: kind, ClaimHandle: claim.Handle, Generation: generation}}
		if err := instance.Validate(); err != nil {
			return instance, action, err
		}
		encoded, err := json.Marshal(instance)
		if err != nil {
			return instance, action, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO compute_managed_endpoint_instances(instance_id,owner_user_id,endpoint_name,approved_hash,start_generation,instance_json,created_at) VALUES(?,?,?,?,?,?,?)`, instance.ID, instance.UserID, name, instance.ApprovedHash, instance.StartGeneration, string(encoded), s.now().UTC()); err != nil {
			return instance, action, err
		}
	} else {
		return instance, action, err
	}
preparedOwner:
	action.Ref = compute.ManagedEndpointActionRef{InstanceID: instance.ID, Kind: kind, ClaimHandle: claim.Handle, Generation: generation}
	if _, err := tx.ExecContext(ctx, `INSERT INTO compute_managed_endpoint_actions(instance_id,kind,claim_generation,claim_handle,created_at) VALUES(?,?,?,?,?) ON CONFLICT(instance_id,kind,claim_generation) DO NOTHING`, instance.ID, kind, generation, claim.Handle, s.now().UTC()); err != nil {
		return instance, action, err
	}
	loaded, err := readManagedEndpointActionTx(ctx, tx, action.Ref)
	if err != nil {
		return instance, action, err
	}
	return instance, loaded, tx.Commit()
}

func readManagedEndpointActionTx(ctx context.Context, tx *sql.Tx, ref compute.ManagedEndpointActionRef) (compute.ManagedEndpointAction, error) {
	action := compute.ManagedEndpointAction{Ref: ref}
	var handle string
	var receipt, observation sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT claim_handle,cancel_requested,accepted,receipt_json,observation_error FROM compute_managed_endpoint_actions WHERE instance_id=? AND kind=? AND claim_generation=?`, ref.InstanceID, ref.Kind, ref.Generation).Scan(&handle, &action.CancelRequested, &action.Accepted, &receipt, &observation)
	if err != nil {
		return action, err
	}
	if handle != ref.ClaimHandle {
		return action, compute.ErrManagedEndpointStaleAction
	}
	if observation.Valid {
		action.ObservationFailure = observation.String
	}
	if receipt.Valid {
		var decoded compute.ManagedEndpointOwnerReceipt
		if err := json.Unmarshal([]byte(receipt.String), &decoded); err != nil {
			return action, err
		}
		if decoded.Ref != ref {
			return action, compute.ErrManagedEndpointStaleAction
		}
		action.Receipt = &decoded
	}
	return action, nil
}

func (s *Store) ReadManagedEndpointAction(ctx context.Context, ref compute.ManagedEndpointActionRef) (compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction, error) {
	var instance compute.ManagedEndpointOwnerInstance
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return instance, compute.ManagedEndpointAction{}, err
	}
	defer tx.Rollback()
	instance, err = readManagedEndpointInstanceTx(ctx, tx, `instance_id=?`, ref.InstanceID)
	if err != nil {
		return instance, compute.ManagedEndpointAction{}, err
	}
	action, err := readManagedEndpointActionTx(ctx, tx, ref)
	if err != nil {
		return instance, action, err
	}
	if action.Receipt == nil && !action.CancelRequested {
		if _, err := s.managedEndpointActionClaimTx(ctx, tx, ref, instance); err != nil {
			return instance, action, err
		}
	}
	return instance, action, tx.Commit()
}

func (s *Store) ReadManagedEndpointLiveOwner(ctx context.Context, userID, name string) (compute.ManagedEndpointOwnerInstance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return compute.ManagedEndpointOwnerInstance{}, err
	}
	defer tx.Rollback()
	instance, err := readManagedEndpointInstanceTx(ctx, tx, `owner_user_id=? AND endpoint_name=? AND retired_at IS NULL`, strings.TrimSpace(userID), strings.TrimSpace(name))
	if errors.Is(err, sql.ErrNoRows) {
		return instance, compute.ErrManagedEndpointOwnershipUnknown
	}
	if err != nil {
		return instance, err
	}
	var state, hash string
	if err := tx.QueryRowContext(ctx, `SELECT state,approved_script_hash FROM compute_managed_endpoints WHERE name=? AND owner_user_id=?`, instance.Endpoint, instance.UserID).Scan(&state, &hash); err != nil {
		return instance, err
	}
	if state != "live" || hash != instance.ApprovedHash || instance.Identity == (compute.ManagedEndpointRuntimeIdentity{}) {
		return instance, compute.ErrManagedEndpointOwnershipUnknown
	}
	return instance, tx.Commit()
}
