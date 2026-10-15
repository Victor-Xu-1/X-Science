//go:build linux

package detached

import workspace "x-science/internal/persistence/workspace"

// Only a positive observed host total proves a hard bound, including a real
// zero after the reserve. Current availability and a generic unknown maximum
// cannot prove infeasibility. A reserved default is an immutable allocation,
// unlike an unreserved default's preference, so check its actual bytes too.
func checkExecutorHostCapacity(backend workspace.KernelExecutionBackend, requested, minimum, reserved int64, observation executorCapacityObservation) error {
	if observation.HostTotalBytes <= 0 {
		return nil
	}
	maximum := max(0, observation.HostTotalBytes-observation.ReserveBytes)
	if max(requested, minimum, reserved) <= maximum {
		return nil
	}
	return &ExecutorResourceBudgetExceedsCurrentCapacityError{
		BackendID: backend.BackendID, Generation: backend.BackendGeneration,
		RequestedBytes: requested, MinimumBytes: minimum, ReservedBytes: reserved,
		MaximumBytes: maximum, HostTotalBytes: observation.HostTotalBytes, ReserveBytes: observation.ReserveBytes,
	}
}
