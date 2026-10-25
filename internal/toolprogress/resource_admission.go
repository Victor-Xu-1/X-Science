package toolprogress

import "time"

const (
	ResourceAdmissionCapacity = "insufficient_capacity"
	ResourceAdmissionFairness = "earlier_waiter"
	ResourceAdmissionGranted  = "admitted"
	maxAdmissionPublicInteger = int64(9007199254740991)
)

// ResourceAdmissionSnapshot contains only bounded admission facts, never process,
// account, path, or other waiters' identities. DecisionAt timestamps the use of
// the planner's capacity/usage inputs; it is not an atomic operating-system sample.
type ResourceAdmissionSnapshot struct {
	DecisionAt             string `json:"decisionAt"`
	Reason                 string `json:"reason"`
	CapacityAvailableBytes int64  `json:"capacityAvailableBytes"`
	UnusedPromisedBytes    int64  `json:"unusedPromisedBytes"`
	NetAvailableBytes      int64  `json:"netAvailableBytes"`
	RequestedBytes         int64  `json:"requestedBytes"`
	RequiredBytes          int64  `json:"requiredBytes"`
	ReservedBytes          int64  `json:"reservedBytes"`
	PrecedingWaiters       int64  `json:"precedingWaiters"`
}

// NormalizeResourceAdmission copies a valid public snapshot. Invalid telemetry
// is omitted, never clamped into a different admission fact or used as authority.
func NormalizeResourceAdmission(snapshot *ResourceAdmissionSnapshot) *ResourceAdmissionSnapshot {
	if snapshot == nil || len(snapshot.DecisionAt) > 35 {
		return nil
	}
	decision, err := time.Parse(time.RFC3339Nano, snapshot.DecisionAt)
	if err != nil || decision.IsZero() {
		return nil
	}
	for _, value := range []int64{snapshot.CapacityAvailableBytes, snapshot.UnusedPromisedBytes,
		snapshot.NetAvailableBytes, snapshot.RequestedBytes, snapshot.RequiredBytes,
		snapshot.ReservedBytes, snapshot.PrecedingWaiters} {
		if value < 0 || value > maxAdmissionPublicInteger {
			return nil
		}
	}
	if snapshot.RequiredBytes <= 0 || snapshot.NetAvailableBytes != max(0, snapshot.CapacityAvailableBytes-snapshot.UnusedPromisedBytes) {
		return nil
	}
	if snapshot.RequestedBytes > 0 && snapshot.RequestedBytes != snapshot.RequiredBytes {
		return nil
	}
	switch snapshot.Reason {
	case ResourceAdmissionFairness:
		if snapshot.PrecedingWaiters == 0 || snapshot.ReservedBytes != 0 {
			return nil
		}
	case ResourceAdmissionCapacity:
		if snapshot.PrecedingWaiters != 0 || snapshot.ReservedBytes != 0 || snapshot.RequiredBytes <= snapshot.NetAvailableBytes {
			return nil
		}
	case ResourceAdmissionGranted:
		if snapshot.PrecedingWaiters != 0 || snapshot.ReservedBytes != snapshot.RequiredBytes || snapshot.ReservedBytes > snapshot.NetAvailableBytes {
			return nil
		}
	default:
		return nil
	}
	result := *snapshot
	result.DecisionAt = decision.UTC().Format(time.RFC3339Nano)
	return &result
}
