package detached

import "time"

// Idle policy is independent of the platform-specific executor and keeps active
// work protected on every platform that verifies the shared result contract.
func executorShouldReapIdle(lastActivity time.Time, activeCount int, now time.Time, timeout time.Duration) bool {
	if activeCount != 0 || timeout <= 0 || lastActivity.IsZero() {
		return false
	}
	return !now.Before(lastActivity) && now.Sub(lastActivity) >= timeout
}
