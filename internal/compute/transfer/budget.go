package transfer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const bytesPerMiB = int64(1 << 20)

// MegabyteBudgets converts only explicit limits. It does not create a default
// dataset budget or shorten the caller's limit to the current machine size.
// Physical storage admission happens at the actual staging/delivery target.
func MegabyteBudgets(fields map[string]any) (fileBytes, totalBytes int64, err error) {
	for name, raw := range fields {
		if name != "max_file_mb" && name != "max_total_mb" {
			return 0, 0, fmt.Errorf("unknown transfer limit %q", name)
		}
		value, parseErr := positiveMegabytes(raw)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("%s: %w", name, parseErr)
		}
		if name == "max_file_mb" {
			fileBytes = value * bytesPerMiB
		} else {
			totalBytes = value * bytesPerMiB
		}
	}
	if fileBytes > 0 && totalBytes > 0 && fileBytes > totalBytes {
		return 0, 0, errors.New("max_file_mb must not exceed max_total_mb")
	}
	return fileBytes, totalBytes, nil
}

func positiveMegabytes(raw any) (int64, error) {
	var value int64
	switch number := raw.(type) {
	case int:
		value = int64(number)
	case int64:
		value = number
	case json.Number:
		var err error
		value, err = number.Int64()
		if err != nil {
			return 0, errors.New("transfer limit must be an integral MiB value")
		}
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 || number > float64(math.MaxInt64/bytesPerMiB) {
			return 0, errors.New("transfer limit must be an integral, representable MiB value")
		}
		value = int64(number)
	default:
		return 0, errors.New("transfer limit must be a numeric integral MiB value")
	}
	if value < 1 || value > math.MaxInt64/bytesPerMiB {
		return 0, errors.New("transfer limit is not a positive representable byte budget")
	}
	return value, nil
}
