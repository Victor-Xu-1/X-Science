package server

import (
	"errors"

	"synon-go/internal/kernel/detached"
)

func kernelResourceAdmissionReceipt(err error) (map[string]any, bool) {
	var unavailable *detached.ExecutorResourceUnavailableError
	if !errors.As(err, &unavailable) {
		return nil, false
	}
	return map[string]any{
		"ok": false, "executed": false, "status": "resource_capacity_unavailable",
		"retryable": true, "execution_outcome": "not_started",
		"available_memory_bytes": unavailable.AvailableBytes,
		"control_reserve_bytes":  unavailable.ReserveBytes,
		"minimum_memory_bytes":   unavailable.RequiredBytes,
		"error":                  "No computation was started because the currently available capacity cannot preserve the controller. Existing task context and outputs remain unchanged.",
	}, true
}
