//go:build linux

package compute

import (
	"context"
	"errors"
)

// This is only a negative admission proof. It does not inspect, stop or claim
// an unrelated service at the registered port/container locator.
func (runtime *ManagedEndpointSystemRuntime) noOwnerCreatedReceipt(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, bool, error) {
	if action.Ref.Kind != "stop" || action.Accepted {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	reserved, err := runtime.store.ReserveManagedEndpointNoOwnerCleanup(ctx, action.Ref)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	if !reserved {
		return ManagedEndpointOwnerReceipt{}, false, nil
	}
	// Bootstrap only an idle observer under the current Stop authority. Its
	// original Start is durably cancelled and is never submitted or accepted.
	worker, err := runtime.worker(ctx, instance, &action.Ref, false)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	if _, err := runtime.call(ctx, instance, worker, "observe", action, nil); err != nil && !errors.Is(err, ErrManagedEndpointStaleAction) && !errors.Is(err, ErrManagedEndpointOutcomeUnknown) {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	owned := instance
	owned.Registration.Owner = &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: instance.Registration.Owner.Output}
	owned.Identity = worker.Identity
	proof, err := runtime.unit.stop(ctx, owned)
	if err != nil {
		return ManagedEndpointOwnerReceipt{}, false, err
	}
	return ManagedEndpointOwnerReceipt{Ref: action.Ref, NoScript: true, NoOwnerCreated: true, Stopped: true, WorkerStoppedProof: proof, OutputWarning: "No Start was admitted for this fresh instance; external same-name or same-port services were not touched."}, true, nil
}
