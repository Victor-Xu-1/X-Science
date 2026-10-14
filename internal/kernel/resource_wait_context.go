package kernel

import "context"

// ResourceAdmissionWaitContextSource retains the requesting runner's lifetime
// when a prepared execution's context deliberately survives request handoff.
// Only unstarted session-admission and resource-queue waits observe this context;
// admitted startup and execution retain their existing detached lifecycle contract.
type ResourceAdmissionWaitContextSource interface {
	ResourceAdmissionWaitContext() context.Context
}

// ResourceCapacityWaitScopeSource optionally accounts for a verified durable
// capacity wait separately from host preparation. Reading cancellation authority
// or waiting for a session lock must never begin this scope.
type ResourceCapacityWaitScopeSource interface {
	// Begin once, only after exact backend and waiting reservation validation.
	// Call the returned cleanup once on every exit, including cancellation.
	BeginResourceCapacityWait() func()
}
