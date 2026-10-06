package server

import (
	"errors"
	"fmt"
	"testing"

	"synon-go/internal/kernel/detached"
)

func TestKernelResourceAdmissionReturnsUnstartedReceiptNotExecutionFailure(t *testing.T) {
	err := fmt.Errorf("prepare executor: %w", &detached.ExecutorResourceUnavailableError{AvailableBytes: 512 << 20, ReserveBytes: 3 << 30, RequiredBytes: 768 << 20})
	receipt, found := kernelResourceAdmissionReceipt(err)
	if !found || receipt["executed"] != false || receipt["execution_outcome"] != "not_started" || receipt["status"] != "resource_capacity_unavailable" || receipt["retryable"] != true {
		t.Fatalf("incorrect capacity receipt: %#v found=%t", receipt, found)
	}
	if _, found := kernelResourceAdmissionReceipt(errors.New("permanent authority failure")); found {
		t.Fatal("permanent authority failure was hidden as resource waiting")
	}
}
