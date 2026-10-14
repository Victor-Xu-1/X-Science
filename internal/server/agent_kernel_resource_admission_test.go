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
