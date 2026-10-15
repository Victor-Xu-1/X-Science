package server

import (
	"errors"
	"fmt"
	"testing"

	"x-science/internal/kernel/detached"
)

func TestKernelResourceAdmissionReturnsUnstartedReceiptNotExecutionFailure(t *testing.T) {
	err := fmt.Errorf("prepare executor: %w", &detached.ExecutorResourceUnavailableError{AvailableBytes: 512 << 20, ReserveBytes: 3 << 30, RequiredBytes: 768 << 20})
	receipt, found := kernelResourceAdmissionReceipt(err)
	if !found || receipt["executed"] != false || receipt["execution_outcome"] != "not_started" || receipt["status"] != "resource_capacity_unavailable" || receipt["retryable"] != true {
		t.Fatalf("incorrect capacity receipt: %#v found=%t", receipt, found)
	}
	if receipt["available_memory_bytes"] != int64(512<<20) || receipt["control_reserve_bytes"] != int64(3<<30) || receipt["minimum_memory_bytes"] != int64(768<<20) {
		t.Fatalf("capacity observation changed in public receipt: %#v", receipt)
	}
	if _, found := kernelResourceAdmissionReceipt(errors.New("permanent authority failure")); found {
		t.Fatal("permanent authority failure was hidden as resource waiting")
	}
}

func TestKernelResourceAdmissionDistinguishesCurrentHostBudgetLimit(t *testing.T) {
	err := fmt.Errorf("prepare executor: %w", &detached.ExecutorResourceBudgetExceedsCurrentCapacityError{
		BackendID: "backend-budget", Generation: 2, RequestedBytes: 10 << 30,
		MaximumBytes: 9 << 30, HostTotalBytes: 12 << 30, ReserveBytes: 3 << 30,
	})
	receipt, found := kernelResourceAdmissionReceipt(err)
	if !found || receipt["ok"] != false || receipt["executed"] != false || receipt["execution_outcome"] != "not_started" || receipt["status"] != "resource_budget_exceeds_current_capacity" || receipt["retryable"] != false || receipt["admission_state"] != "budget_exceeds_current_host_capacity" {
		t.Fatalf("host-infeasible budget became an ordinary retryable wait: %#v found=%t", receipt, found)
	}
	for key, want := range map[string]any{
		"backend_id": "backend-budget", "backend_generation": int64(2),
		"requested_memory_bytes": int64(10 << 30), "maximum_memory_bytes": int64(9 << 30),
		"host_total_memory_bytes": int64(12 << 30), "control_reserve_bytes": int64(3 << 30),
		"recovery": "recheck_after_host_capacity_changes_or_submit_a_new_operation_with_a_different_explicit_budget_or_choose_an_authorized_different_runtime",
	} {
		if receipt[key] != want {
			t.Errorf("receipt[%s]=%v, want %v", key, receipt[key], want)
		}
	}
}

func TestKernelResourceAdmissionKeepsRequestSeparateFromInfeasibleMinimum(t *testing.T) {
	for _, requested := range []int64{0, 1 << 30} {
		t.Run(fmt.Sprintf("requested_%d", requested), func(t *testing.T) {
			observation := &detached.ExecutorResourceBudgetExceedsCurrentCapacityError{
				BackendID: "backend-default-floor", Generation: 3,
				RequestedBytes: requested, MinimumBytes: 768 << 20,
				MaximumBytes: 512 << 20, HostTotalBytes: 3584 << 20, ReserveBytes: 3 << 30,
			}
			receipt, found := kernelResourceAdmissionReceipt(fmt.Errorf("prepare executor: %w", observation))
			if !found || receipt["executed"] != false || receipt["execution_outcome"] != "not_started" ||
				receipt["retryable"] != false || receipt["status"] != "resource_budget_exceeds_current_capacity" {
				t.Fatalf("infeasible runtime minimum became an execution or retry: %#v", receipt)
			}
			for key, want := range map[string]any{
				"requested_memory_bytes": requested, "minimum_memory_bytes": int64(768 << 20),
				"required_memory_bytes": max(requested, int64(768<<20)), "maximum_memory_bytes": int64(512 << 20),
				"backend_id": "backend-default-floor", "backend_generation": int64(3),
				"recovery": "recheck_after_host_capacity_changes_or_choose_an_authorized_different_runtime",
			} {
				if receipt[key] != want {
					t.Errorf("receipt[%s]=%v, want %v", key, receipt[key], want)
				}
			}
			if requested == 0 && receipt["error"] != "The minimum executor memory requirement exceeds the observed host total after the controller reserve. Freeing current usage alone cannot fit it under this observation. No computation was started and the default allocation request remains unchanged." {
				t.Errorf("default request was incorrectly described as an excessive explicit budget: %v", receipt["error"])
			}
		})
	}
}
