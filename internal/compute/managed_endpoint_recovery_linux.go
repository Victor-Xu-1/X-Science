//go:build linux

package compute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Once the exact native owner is positively gone, recover the original command
// receipt if present. Missing command exit evidence is reported as missing; a
// stop observation does not invent exit 0 or replay the approved script.
func (runtime *ManagedEndpointSystemRuntime) stoppedNativeReceipt(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, bool, error) {
	if instance.Registration.Owner.Kind != "native-unit" {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	observed, err := runtime.unit.observe(ctx, instance)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	if !observed.Absent && !observed.Empty {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	if instance.Identity.Kind != "native-unit" {
		return ManagedEndpointOwnerReceipt{}, false, ErrManagedEndpointOutcomeUnknown
	}
	proof, err := runtime.unit.stop(ctx, instance)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	receipt := ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: action.Accepted, NoScript: true, Identity: instance.Identity, OwnerVerified: true}
	var journal ManagedEndpointAction
	if err := readManagedEndpointPrivateJSON(filepath.Join(managedEndpointInstanceDirectory(instance), "action.json"), &journal); err == nil {
		if journal.Ref == action.Ref && journal.Receipt != nil {
			if journal.Receipt.Ref != action.Ref {
				return receipt, false, ErrManagedEndpointCorruptOwner
			}
			if identity := journal.Receipt.Identity; identity != (ManagedEndpointRuntimeIdentity{}) && identity.ValidateFor(instance) != nil {
				return receipt, false, ErrManagedEndpointCorruptOwner
			}
			receipt = *journal.Receipt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return receipt, false, err
	}
	if receipt.NoScript && action.Accepted {
		receipt.OutputWarning = "The accepted action's command exit was not recovered; this receipt only observes its original physical owner stopped."
	}
	if action.Ref.Kind == "start" && receipt.Failure == "" {
		receipt.Failure = "original native endpoint owner exited before live handoff"
	}
	receipt.Identity = instance.Identity
	receipt.OwnerVerified = true
	receipt.Stopped = true
	receipt.StoppedProof = proof
	return receipt, true, nil
}

func (runtime *ManagedEndpointSystemRuntime) stoppedDockerReceipt(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, bool, error) {
	if action.Ref.Kind != "stop" || instance.Registration.Owner.Kind != "docker" {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	docker, err := newManagedEndpointDocker()
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	_, state, inspectErr := docker.inspect(ctx, instance)
	if inspectErr == nil && (state.State.Running || state.State.Restarting) {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	proof, err := docker.observeStopped(ctx, instance)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	worker, err := runtime.unit.observe(ctx, instance)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	if !worker.Absent && !worker.Empty {
		if instance.WorkerIdentity == (ManagedEndpointRuntimeIdentity{}) || worker.Identity != instance.WorkerIdentity {
			return ManagedEndpointOwnerReceipt{}, false, ErrManagedEndpointOwnershipUnknown
		}
		owned := instance
		owned.Registration.Owner = &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
		owned.Identity = worker.Identity
		if _, err := runtime.unit.stop(ctx, owned); err != nil {
			return ManagedEndpointOwnerReceipt{}, false, err
		}
	}
	receipt := ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: action.Accepted, NoScript: true, Identity: proof.Identity, OwnerVerified: true, Stopped: true, StoppedProof: proof}
	if action.Accepted {
		receipt.OutputWarning = "The original physical container is stopped; no accepted script was replayed to obtain this observation."
	}
	return receipt, true, nil
}
