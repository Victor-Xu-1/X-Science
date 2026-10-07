package kernelcontract

import (
	"encoding/json"
	"math"
	"testing"
)

func TestResourceMemoryBudgetPreservesLargeExplicitRequestsAndRejectsInvalidNumbers(t *testing.T) {
	for _, raw := range []any{int64(128 * 1024), json.Number("131072"), float64(131072)} {
		bytes, err := MemoryBudgetBytes(map[string]any{"memory_budget_mb": raw})
		if err != nil || bytes != 128<<30 {
			t.Fatal(bytes, err)
		}
	}
	for _, raw := range []any{-1, 0, 1.5, "1000", math.NaN(), math.Inf(1), int64(math.MaxInt64)} {
		if _, err := MemoryBudgetBytes(map[string]any{"memory_budget_mb": raw}); err == nil {
			t.Fatalf("invalid allocation %v accepted", raw)
		}
	}
	if bytes, err := MemoryBudgetBytes(nil); bytes != 0 || err != nil {
		t.Fatal(bytes, err)
	}
}
