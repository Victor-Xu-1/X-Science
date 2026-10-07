package server

import (
	"encoding/json"
	"math"

	kernelruntime "synon-go/internal/kernel"
)

func validateComputeProviderProbeReceipt(probe map[string]any) error {
	invalid := func() error {
		return &kernelruntime.ProviderOperationError{Kind: "result_rejected", Message: "provider returned no valid execution receipt; outcome cannot be inferred"}
	}
	ready, ok := probe["ready"].(bool)
	if !ok {
		return invalid()
	}
	if !ready {
		return nil
	}
	if message, ok := probe["phase_read_error"].(string); ok && message != "" {
		return nil
	}
	exit, ok := computeReceiptInteger(probe["job_exit_code"])
	if !ok || exit < 0 || exit > 255 {
		return invalid()
	}
	wall, ok := computeReceiptInteger(probe["job_wall_s"])
	if !ok || wall < 0 {
		return invalid()
	}
	for _, key := range []string{"deadline_fired", "job_timeout_fired"} {
		if value, exists := probe[key]; exists {
			if _, ok := value.(bool); !ok {
				return invalid()
			}
		}
	}
	return nil
}

func computeReceiptInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value >= float64(math.MaxInt64) || value < float64(math.MinInt64) {
			return 0, false
		}
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
