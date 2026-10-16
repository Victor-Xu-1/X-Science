package detached

// An explicit budget below the observed executor minimum cannot be repaired by
// waiting for free capacity. A new admitted request must change that budget;
// silently increasing it would violate the immutable execution allocation.
type ExecutorResourceBudgetBelowMinimumError struct {
	BackendID      string
	Generation     int64
	RequestedBytes int64
	MinimumBytes   int64
}

func (err *ExecutorResourceBudgetBelowMinimumError) Error() string {
	return "the requested executor memory budget is below the required runtime minimum"
}

// This observed host upper bound excludes the controller reserve, not current
// usage. Waiting for usage to fall cannot satisfy this allocation on the
// observed host, but a later capacity change may make it feasible.
type ExecutorResourceBudgetExceedsCurrentCapacityError struct {
	BackendID      string
	Generation     int64
	RequestedBytes int64
	MinimumBytes   int64
	ReservedBytes  int64
	MaximumBytes   int64
	HostTotalBytes int64
	ReserveBytes   int64
}

func (err *ExecutorResourceBudgetExceedsCurrentCapacityError) Error() string {
	return "the required executor memory exceeds the observed host capacity after the control-plane reserve"
}

// Keep the immutable request (including default zero) separate from the
// observed runtime floor and an already-admitted allocation. An unreserved
// default preference is not a mandatory budget; a reserved default cannot shrink.
func (err *ExecutorResourceBudgetExceedsCurrentCapacityError) RequiredBytes() int64 {
	return max(err.RequestedBytes, err.MinimumBytes, err.ReservedBytes)
}

// Capacity failure is an unstarted execution unit, not an application result.
// Keep it typed across the backend/server boundary so callers can retain the
// logical task and select another authorized resource without claiming a run.
type ExecutorResourceUnavailableError struct {
	BackendID      string
	Generation     int64
	AvailableBytes int64
	ReserveBytes   int64
	RequiredBytes  int64
}

func (err *ExecutorResourceUnavailableError) Error() string {
	return "detached execution capacity is currently unavailable while preserving the control-plane reserve"
}
