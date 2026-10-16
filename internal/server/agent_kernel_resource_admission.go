package server

import (
	"errors"

	"x-science/internal/kernel/detached"
)

func agentKernelMemoryBudgetSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": "Optional explicit executor memory allocation in MiB. This is a requested budget, not a verified scientific minimum. Admission uses actual host capacity and durable reservations; temporary capacity shortages wait without starting or reducing the computation. Budgets exceeding the observed host limit require changed capacity or a new allocation."}
}

func kernelResourceAdmissionReceipt(err error) (map[string]any, bool) {
	var belowMinimum *detached.ExecutorResourceBudgetBelowMinimumError
	if errors.As(err, &belowMinimum) {
		return map[string]any{
			"backend_id": belowMinimum.BackendID, "backend_generation": belowMinimum.Generation,
			"admission_state": "budget_below_executor_minimum",
			"ok":              false, "executed": false, "status": "resource_budget_below_minimum",
			"retryable": false, "execution_outcome": "not_started",
			"requested_memory_bytes": belowMinimum.RequestedBytes, "minimum_memory_bytes": belowMinimum.MinimumBytes,
			"error":    "The requested memory allocation is below the observed executor minimum. No computation was started and no budget was silently increased.",
			"recovery": "submit_a_new_operation_with_an_explicit_budget_at_least_minimum_memory_bytes_or_choose_an_authorized_different_runtime",
		}, true
	}
	var exceeds *detached.ExecutorResourceBudgetExceedsCurrentCapacityError
	if errors.As(err, &exceeds) {
		receipt := map[string]any{
			"backend_id": exceeds.BackendID, "backend_generation": exceeds.Generation,
			"admission_state": "budget_exceeds_current_host_capacity",
			"ok":              false, "executed": false, "status": "resource_budget_exceeds_current_capacity",
			"retryable": false, "execution_outcome": "not_started",
			"requested_memory_bytes": exceeds.RequestedBytes, "maximum_memory_bytes": exceeds.MaximumBytes,
			"host_total_memory_bytes": exceeds.HostTotalBytes, "control_reserve_bytes": exceeds.ReserveBytes,
			"error":    "The requested memory allocation exceeds the observed host total after the controller reserve. Freeing current usage alone cannot fit it under this observation. No computation was started and no budget was silently reduced.",
			"recovery": "recheck_after_host_capacity_changes_or_submit_a_new_operation_with_a_different_explicit_budget_or_choose_an_authorized_different_runtime",
		}
		if exceeds.MinimumBytes > 0 {
			receipt["minimum_memory_bytes"] = exceeds.MinimumBytes
		}
		if required := exceeds.RequiredBytes(); required > 0 {
			receipt["required_memory_bytes"] = required
		}
		if exceeds.ReservedBytes > 0 {
			receipt["reserved_memory_bytes"] = exceeds.ReservedBytes
		}
		if exceeds.RequestedBytes == 0 {
			receipt["error"] = "The minimum executor memory requirement exceeds the observed host total after the controller reserve. Freeing current usage alone cannot fit it under this observation. No computation was started and the default allocation request remains unchanged."
			if exceeds.ReservedBytes > 0 && exceeds.ReservedBytes > exceeds.MaximumBytes {
				receipt["error"] = "The previously reserved executor memory allocation exceeds the observed host total after the controller reserve. Freeing current usage alone cannot fit it under this observation. No computation was started and neither the default allocation request nor the original reservation was changed."
			}
		}
		if exceeds.MinimumBytes > exceeds.MaximumBytes {
			// No smaller explicit budget can satisfy this observed runtime floor.
			receipt["recovery"] = "recheck_after_host_capacity_changes_or_choose_an_authorized_different_runtime"
		}
		return receipt, true
	}
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
