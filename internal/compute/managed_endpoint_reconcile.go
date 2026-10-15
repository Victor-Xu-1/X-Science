package compute

import (
	"context"
	"fmt"
)

// Dependency availability belongs to one durable intent, not the whole page.
// Accepted work is never resubmitted; no-admission/native cancellation has no
// new approved lifecycle script to run and therefore needs no start credential.
func (coordinator *ManagedEndpointCoordinator) reconcileAction(ctx context.Context, instance ManagedEndpointOwnerInstance, action ManagedEndpointAction) (ManagedEndpointOwnerReceipt, error) {
	if action.CancelRequested && (!action.Accepted || instance.Registration.Owner.Kind == "native-unit") {
		return coordinator.Runtime.Cancel(ctx, instance, action, nil)
	}
	if action.Accepted && !action.CancelRequested {
		return coordinator.Runtime.Observe(ctx, instance, action)
	}
	environment, environmentErr := coordinator.Environment(ctx, instance, action.Ref.Kind)
	if environmentErr != nil {
		if action.Ref.Kind == "stop" || action.CancelRequested {
			// A fresh instance which never admitted Start, or an exact owner
			// already positively stopped, can be observed without credentials.
			// This never authorizes dispatching the approved Stop bytes.
			receipt, observeErr := coordinator.Runtime.Observe(ctx, instance, action)
			if observeErr == nil && receipt.NoScript && (receipt.NoOwnerCreated || receipt.HasPositiveStoppedProof(instance)) {
				return receipt, nil
			}
			if observeErr != nil {
				// Do not classify a missing dependency as ordinary ActionPending:
				// the per-ref observation failure must remain visible in the Store.
				return receipt, fmt.Errorf("resolve approved endpoint environment: %w; current owner observation: %v", environmentErr, observeErr)
			}
		}
		return ManagedEndpointOwnerReceipt{}, environmentErr
	}
	if action.CancelRequested {
		return coordinator.Runtime.Cancel(ctx, instance, action, environment)
	}
	if action.Ref.Kind == "start" {
		return coordinator.Runtime.Start(ctx, instance, action, environment)
	}
	return coordinator.Runtime.Stop(ctx, instance, action, environment)
}
