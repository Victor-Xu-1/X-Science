package software

import (
	"math"
	"testing"
)

func TestResourceBudgetAndLongExplicitDeadlineDoNotForkSoftwareEnvironment(t *testing.T) {
	original := validRequest()
	original.TimeoutSeconds = 10 * 24 * 60 * 60
	base, err := EnvironmentName(LocalProviderID, original)
	if err != nil {
		t.Fatal(err)
	}
	request := original
	request.MemoryBudgetMB = 128 * 1024
	normalized, err := NormalizeRequest(request)
	if err != nil || normalized.TimeoutSeconds != original.TimeoutSeconds || normalized.MemoryBudgetMB != request.MemoryBudgetMB {
		t.Fatal(normalized, err)
	}
	current, err := EnvironmentName(LocalProviderID, request)
	if err != nil || current != base {
		t.Fatal("allocation budget changed software environment identity", current, base, err)
	}
	for _, invalid := range []Request{func() Request { x := request; x.MemoryBudgetMB = math.MaxInt64; return x }(), func() Request { x := request; x.TimeoutSeconds = math.MaxInt64; return x }()} {
		if _, err := NormalizeRequest(invalid); err == nil {
			t.Fatal("unrepresentable resource budget accepted")
		}
	}
}
