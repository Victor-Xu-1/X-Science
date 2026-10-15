package compute

import (
	"context"
	"errors"
	"time"
)

var errManagedEndpointStartupBudgetInvalid = errors.New("managed endpoint startup timeout must be nonnegative and fit time.Duration")

func managedEndpointStartupBudget(seconds int64) (time.Duration, error) {
	// This is only the representational limit, not a host/task capacity policy.
	if seconds < 0 || seconds > int64(time.Duration(1<<63-1)/time.Second) {
		return 0, errManagedEndpointStartupBudgetInvalid
	}
	if seconds == 0 {
		return 2 * time.Minute, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

// Action execution is independent of short control/observation waits and the
// lifetime of a healthy service. Both the script and readiness share this context.
func managedEndpointActionContext(parent context.Context, kind string, owner *ManagedEndpointOwnerSpec) (context.Context, context.CancelFunc) {
	budget := time.Minute
	if kind != "stop" {
		seconds := int64(0)
		if owner != nil {
			seconds = owner.StartupTimeoutSeconds
		}
		var err error
		budget, err = managedEndpointStartupBudget(seconds)
		if err != nil {
			ctx, cancel := context.WithCancelCause(parent)
			cancel(err)
			return ctx, func() { cancel(nil) }
		}
	}
	return context.WithTimeout(parent, budget)
}
