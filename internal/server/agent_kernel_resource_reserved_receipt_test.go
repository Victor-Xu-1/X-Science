package server

import (
	"fmt"
	"testing"

	"x-science/internal/kernel/detached"
)

func TestKernelResourceAdmissionKeepsReservedDefaultSeparateFromMinimum(t *testing.T) {
	observation := &detached.ExecutorResourceBudgetExceedsCurrentCapacityError{
		BackendID: "backend-reserved-default", Generation: 2, RequestedBytes: 0,
		MinimumBytes: 768 << 20, ReservedBytes: 9 << 30,
		MaximumBytes: 6 << 30, HostTotalBytes: 10 << 30, ReserveBytes: 4 << 30,
	}
	original := *observation
	receipt, found := kernelResourceAdmissionReceipt(fmt.Errorf("prepare executor: %w", observation))
	if !found || receipt["ok"] != false || receipt["executed"] != false || receipt["execution_outcome"] != "not_started" ||
		receipt["retryable"] != false || receipt["status"] != "resource_budget_exceeds_current_capacity" ||
		receipt["admission_state"] != "budget_exceeds_current_host_capacity" {
		t.Fatalf("reserved default was mistaken for execution or an ordinary wait: %#v found=%t", receipt, found)
	}
	for key, want := range map[string]any{
		"backend_id": observation.BackendID, "backend_generation": int64(2),
		"requested_memory_bytes": int64(0), "minimum_memory_bytes": int64(768 << 20),
		"reserved_memory_bytes": int64(9 << 30), "required_memory_bytes": int64(9 << 30),
		"maximum_memory_bytes": int64(6 << 30), "host_total_memory_bytes": int64(10 << 30), "control_reserve_bytes": int64(4 << 30),
		"recovery": "recheck_after_host_capacity_changes_or_submit_a_new_operation_with_a_different_explicit_budget_or_choose_an_authorized_different_runtime",
		"error":    "The previously reserved executor memory allocation exceeds the observed host total after the controller reserve. Freeing current usage alone cannot fit it under this observation. No computation was started and neither the default allocation request nor the original reservation was changed.",
	} {
		if receipt[key] != want {
			t.Errorf("receipt[%s]=%v, want %v", key, receipt[key], want)
		}
	}
	if observation.RequiredBytes() != 9<<30 || *observation != original {
		t.Fatalf("projection changed the immutable request/minimum/reservation: %+v want=%+v", observation, original)
	}
}
