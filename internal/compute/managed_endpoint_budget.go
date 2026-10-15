package compute

import (
	"context"
	"time"
)

// Action execution is independent of short control/observation waits and the
// lifetime of a healthy service. Both the script and readiness share this context.
func managedEndpointActionContext(parent context.Context, kind string, _ *ManagedEndpointOwnerSpec) (context.Context, context.CancelFunc) {
	budget := 2 * time.Minute
	if kind == "stop" {
		budget = time.Minute
	}
	return context.WithTimeout(parent, budget)
}
