package server

import "testing"

func TestComputeTransferLimitsPreserveExplicitLargeBudgets(t *testing.T) {
	_, limits, err := agentComputeHarvestContract(map[string]any{
		"transfer_limits": map[string]any{"max_file_mb": int64(80 << 10), "max_total_mb": int64(120 << 10)},
	})
	if err != nil {
		t.Fatalf("explicit transfer budget above the former product ceiling was rejected: %v", err)
	}
	if int64(numberValue(limits["max_file_bytes"])) != 80<<30 || int64(numberValue(limits["max_total_bytes"])) != 120<<30 {
		t.Fatalf("explicit budget was changed: %#v", limits)
	}
}

func TestComputeTransferLimitsDoNotInventAnUnrequestedDatasetBudget(t *testing.T) {
	_, limits, err := agentComputeHarvestContract(nil)
	if err != nil {
		t.Fatal(err)
	}
	if numberValue(limits["max_file_bytes"]) != 0 || numberValue(limits["max_total_bytes"]) != 0 {
		t.Fatalf("an omitted transfer budget invented a dataset ceiling: %#v", limits)
	}
}
