package server

import (
	"errors"

	"synon-go/internal/kernel/detached"
)

func agentKernelMemoryBudgetSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": "Optional explicit executor memory allocation in MiB. This is a requested budget, not a verified scientific minimum. Admission uses actual host capacity and durable reservations; unavailable capacity waits without starting or reducing the computation."}
}

func kernelResourceAdmissionReceipt(err error) (map[string]any, bool) {
	var unavailable *detached.ExecutorResourceUnavailableError
	if !errors.As(err, &unavailable) {
		return nil, false
	}
	return map[string]any{
		"backend_id": unavailable.BackendID, "backend_generation": unavailable.Generation, "admission_state": "waiting_for_capacity",
		"ok": false, "executed": false, "status": "resource_capacity_unavailable",
		"retryable": true, "execution_outcome": "not_started",
		"available_memory_bytes": unavailable.AvailableBytes,
		"control_reserve_bytes":  unavailable.ReserveBytes,
		"minimum_memory_bytes":   unavailable.RequiredBytes,
		"error":                  "No computation was started because the currently available capacity cannot preserve the controller. Existing task context and outputs remain unchanged.",
	}, true
}
