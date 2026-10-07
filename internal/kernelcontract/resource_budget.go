package kernelcontract

import (
	"encoding/json"
	"errors"
	"math"
)

// MemoryBudgetBytes is an explicit allocation request, not a verified claim
// about what a scientific method needs. Omission leaves native admission in
// charge. Representability, not the current machine, bounds the interface.
func MemoryBudgetBytes(input map[string]any) (int64, error) {
	raw, found := input["memory_budget_mb"]
	if !found {
		return 0, nil
	}
	var value int64
	switch typed := raw.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, errors.New("memory_budget_mb must be an integer")
		}
		value = parsed
	case int:
		value = int64(typed)
	case int64:
		value = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed <= 0 || typed > float64(math.MaxInt64>>20) {
			return 0, errors.New("memory_budget_mb invalid")
		}
		value = int64(typed)
	default:
		return 0, errors.New("memory_budget_mb must be an integer")
	}
	if value <= 0 || value > math.MaxInt64>>20 {
		return 0, errors.New("memory_budget_mb is not representable")
	}
	return value << 20, nil
}
