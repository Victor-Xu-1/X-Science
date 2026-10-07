package sciencecapability

import (
	"math"
	"testing"
)

func TestResourceBudgetCarriesThroughTheRegisteredSoftwareRequest(t *testing.T) {
	input := validVinaExecutionRequest()
	input.MemoryBudgetMB = 128 * 1024
	input.TimeoutSeconds = 10 * 24 * 60 * 60
	request, _, _, _, err := BuildSoftwareRequest(input)
	if err != nil || request.MemoryBudgetMB != input.MemoryBudgetMB || request.TimeoutSeconds != input.TimeoutSeconds {
		t.Fatal(request.MemoryBudgetMB, request.TimeoutSeconds, err)
	}
	input.MemoryBudgetMB = math.MaxInt64
	if _, _, _, err := NormalizeExecutionRequest(input); err == nil {
		t.Fatal("unrepresentable allocation was admitted")
	}
}
