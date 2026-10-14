package server

import (
	"encoding/json"

	"x-science/internal/toolcontract"
)

// Use the projection's hash-validated original result, not its storage locator.
// An unavailable original is not material evidence, even if a stale value is
// present. Inline legacy results retain their existing progress semantics;
// unresolved descriptors use the canonical closed contract, never an outcome
// guess from their preview or presentation metadata.
func runnerRecoveryMaterialResult(value any, restored, unavailable bool) any {
	if unavailable {
		return nil
	}
	if restored {
		return value
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if _, _, externalized, _ := toolcontract.DecodeExternalizedResult(raw); externalized {
		return nil
	}
	return value
}
