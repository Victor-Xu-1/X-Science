package detached

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
