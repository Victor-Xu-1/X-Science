//go:build linux

package compute

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"
)

// A positively observed PID is not proof that exec and private protocol startup
// finished. Probe only identity under the caller's one startup budget. Never
// swap the expected incarnation, inspect an action as readiness, or retry an
// action/launch to turn a missing control response into execution permission.
func (runtime *ManagedEndpointSystemRuntime) awaitWorkerControl(ctx context.Context, instance ManagedEndpointOwnerInstance, expected managedEndpointUnitObservation) (managedEndpointUnitObservation, error) {
	for {
		_, probeErr := runtime.call(ctx, instance, expected, "hello", ManagedEndpointAction{}, nil)
		if probeErr == nil {
			current, err := runtime.unit.observe(ctx, instance)
			if err != nil {
				return current, err
			}
			if !managedEndpointSameReadyWorker(current, expected) {
				return current, ErrManagedEndpointOwnershipUnknown
			}
			return current, nil
		}
		if !managedEndpointSocketNotListening(probeErr) {
			return expected, probeErr
		}
		select {
		case <-ctx.Done():
			return expected, errors.Join(context.Cause(ctx), probeErr)
		case <-time.After(50 * time.Millisecond):
		}
		current, err := runtime.unit.observe(ctx, instance)
		if err != nil {
			return current, err
		}
		if !managedEndpointSameReadyWorker(current, expected) {
			return current, ErrManagedEndpointOwnershipUnknown
		}
	}
}

func managedEndpointSameReadyWorker(current, expected managedEndpointUnitObservation) bool {
	return !current.Absent && current.State != "inactive" && current.State != "failed" &&
		current.Identity.PID > 0 && current.Identity == expected.Identity
}

func managedEndpointSocketNotListening(err error) bool {
	var operation *net.OpError
	return errors.As(err, &operation) && operation.Op == "dial" &&
		(errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED))
}
