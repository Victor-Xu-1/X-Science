package server

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestKernelHostIntegerNormalizationUsesPlatformBounds(t *testing.T) {
	for _, value := range []int64{0, -41, 41, math.MinInt, math.MaxInt} {
		normalized, err := normalizeKernelHostValue(json.Number(strconv.FormatInt(value, 10)), 0, &kernelHostJSONBudget{})
		integer, ok := normalized.(int)
		if err != nil || !ok || int64(integer) != value {
			t.Fatalf("integer %d changed: %v err=%v", value, normalized, err)
		}
	}
	if strconv.IntSize == 32 {
		for _, value := range []int64{math.MinInt32 - 1, math.MaxInt32 + 1} {
			if _, err := normalizeKernelHostValue(json.Number(strconv.FormatInt(value, 10)), 0, &kernelHostJSONBudget{}); err == nil {
				t.Fatalf("integer %d narrowed without an error", value)
			}
		}
	}
}
