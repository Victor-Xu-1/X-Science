package kernel

import "context"

// ResourceAdmissionWaitContextSource retains the requesting runner's lifetime
// when a prepared execution's context deliberately survives request handoff.
// Only an unstarted, durable resource-queue wait observes this context; admitted
// startup and execution retain their existing detached lifecycle contract.
type ResourceAdmissionWaitContextSource interface {
	// The cleanup ends this one queue-wait scope. It must be called on every
	// exit, including cancellation; polling does not open additional scopes.
	ResourceAdmissionWaitContext() (context.Context, func())
}
