package failurecontract

import "testing"

func TestNextActionMapsClosedFailureKindsToOrchestrationBoundaries(t *testing.T) {
	cases := map[Kind]string{
		Unauthorized:         "wait_for_user_authority",
		RateLimited:          "wait_for_external_state_then_start_new_execution",
		QuotaExhausted:       "wait_for_external_state_then_start_new_execution",
		Transient:            "wait_for_external_state_then_start_new_execution",
		NetworkBridgeDown:    "wait_for_external_state_then_start_new_execution",
		ProviderDegraded:     "wait_for_external_state_then_start_new_execution",
		NotFound:             "inspect_contract_then_start_new_execution",
		InvalidRequest:       "inspect_contract_then_start_new_execution",
		ImageBuildFailed:     "inspect_contract_then_start_new_execution",
		NetworkDenied:        "inspect_contract_then_start_new_execution",
		OwnershipMismatch:    "inspect_contract_then_start_new_execution",
		ResultRejected:       "inspect_contract_then_start_new_execution",
		Kind("unknown_kind"): "inspect_contract_then_start_new_execution",
	}
	for kind, want := range cases {
		if got := NextAction(kind); got != want {
			t.Fatalf("NextAction(%q)=%q want=%q", kind, got, want)
		}
	}
}

func TestValidRejectsUnknownFailureKinds(t *testing.T) {
	for _, kind := range []Kind{NotFound, Transient, ResultRejected} {
		if !Valid(kind) {
			t.Fatalf("known kind %q was rejected", kind)
		}
	}
	if Valid(Kind("")) || Valid(Kind("model_prose_failure")) {
		t.Fatal("unknown failure kinds must not enter the closed algebra")
	}
}
