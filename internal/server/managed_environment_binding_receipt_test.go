package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestManagedEnvironmentBindingReceiptRetainsAdmittedIdentity(t *testing.T) {
	input := map[string]any{"implementation": "Registry Engine@1.2", "name": "requested"}
	receipt := managedEnvironmentBindingReceipt("manage_environments", input)
	input["implementation"] = "later different choice"
	if receipt["version"] != 1 || receipt["tool"] != "manage_environments" ||
		!reflect.DeepEqual(receipt["input"], map[string]any{"implementation": "Registry Engine@1.2", "name": "requested"}) {
		t.Fatalf("binding rewrote or aliased its admitted identity: %#v", receipt)
	}
}

func TestEnvironmentMismatchDiagnosticPreservesExactBindingWithinBudget(t *testing.T) {
	for _, huge := range []bool{false, true} {
		preflight := map[string]any{"status": "selected_implementation_environment_mismatch",
			"environment": "ready-runtime", "environment_implementations": []string{"Engine A 1.2.7"},
			"selected_implementations": []string{"Engine A"}, "arguments": "private", "token": "private"}
		if huge {
			preflight["recovery"] = strings.Repeat("Explain the same repair. ", 1000)
		}
		raw := agentRuntimePreflightDiagnostic(preflight)
		var diagnostic map[string]any
		if err := json.Unmarshal([]byte(raw), &diagnostic); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 1800 || diagnostic["environment"] != "ready-runtime" ||
			!reflect.DeepEqual(diagnostic["environment_implementations"], []any{"Engine A 1.2.7"}) ||
			!reflect.DeepEqual(diagnostic["selected_implementations"], []any{"Engine A"}) || diagnostic["arguments"] != nil || diagnostic["token"] != nil {
			t.Fatalf("diagnostic changed identities or exposed arguments: %s", raw)
		}
		if huge && diagnostic["diagnostic_truncated"] != true {
			t.Fatal("bounded prose truncation was not explicit")
		}
	}
}
