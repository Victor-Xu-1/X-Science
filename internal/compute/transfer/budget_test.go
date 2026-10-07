package transfer

import (
	"encoding/json"
	"math"
	"testing"
)

func TestMegabyteBudgetsKeepExplicitValuesAndOmission(t *testing.T) {
	file, total, err := MegabyteBudgets(map[string]any{"max_file_mb": json.Number("81920"), "max_total_mb": float64(122880)})
	if err != nil || file != 80<<30 || total != 120<<30 {
		t.Fatalf("budget conversion = %d %d %v", file, total, err)
	}
	file, total, err = MegabyteBudgets(nil)
	if err != nil || file != 0 || total != 0 {
		t.Fatalf("omission invented a budget = %d %d %v", file, total, err)
	}
}

func TestMegabyteBudgetsRejectFractionsOverflowAndUnknownFields(t *testing.T) {
	for _, raw := range []any{0, -1, 1.5, math.Inf(1), math.NaN(), int64(math.MaxInt64), "100", json.Number("1.5")} {
		if _, _, err := MegabyteBudgets(map[string]any{"max_total_mb": raw}); err == nil {
			t.Fatalf("invalid budget admitted: %#v", raw)
		}
	}
	if _, _, err := MegabyteBudgets(map[string]any{"unknown": 1}); err == nil {
		t.Fatal("unknown field admitted")
	}
	if _, _, err := MegabyteBudgets(map[string]any{"max_file_mb": 2, "max_total_mb": 1}); err == nil {
		t.Fatal("contradictory budgets admitted")
	}
}
