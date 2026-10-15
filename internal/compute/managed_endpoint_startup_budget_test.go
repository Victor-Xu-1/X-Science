package compute

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestManagedEndpointStartupBudgetSurvivesApprovedRegistration(t *testing.T) {
	const original = `{"name":"startup-policy","url":"http://127.0.0.1:23171","port":23171,"skillName":"managed-endpoint-policy","startScript":":","stopScript":":","livePath":"/health","owner":{"version":1,"kind":"native-unit","output":{"segmentBytes":4096,"segments":2}}}`
	decode := func(seconds string) (ManagedEndpointRegistration, error) {
		raw := original
		if seconds != "" {
			raw = strings.TrimSuffix(raw, "}}") + `,"startupTimeoutSeconds":` + seconds + `}}`
		}
		var registration ManagedEndpointRegistration
		err := json.Unmarshal([]byte(raw), &registration)
		return registration, err
	}
	baseline, err := decode("")
	if err != nil || baseline.Owner == nil {
		t.Fatalf("decode original registration: owner=%#v err=%v", baseline.Owner, err)
	}
	if err := baseline.Owner.Validate(); err != nil {
		t.Fatalf("original owner is invalid: %v", err)
	}

	t.Run("omitted_budget_preserves_existing_approval_bytes", func(t *testing.T) {
		raw, err := json.Marshal(baseline)
		if err != nil || string(raw) != original {
			t.Fatalf("original registration bytes changed: raw=%s err=%v", raw, err)
		}
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
		if got := ApprovedManagedEndpointHash(baseline); got != want {
			t.Fatalf("original approval hash=%s want=%s", got, want)
		}
	})

	t.Run("explicit_budget_roundtrips_and_changes_approval", func(t *testing.T) {
		registration, err := decode("300")
		if err != nil || registration.Owner == nil {
			t.Fatalf("decode explicit startup budget: owner=%#v err=%v", registration.Owner, err)
		}
		if err := registration.Owner.Validate(); err != nil {
			t.Fatalf("finite approved startup budget is invalid: %v", err)
		}
		raw, err := json.Marshal(registration.Owner)
		if err != nil {
			t.Fatal(err)
		}
		var owner map[string]json.RawMessage
		if err := json.Unmarshal(raw, &owner); err != nil {
			t.Fatal(err)
		}
		if got := string(owner["startupTimeoutSeconds"]); got != "300" {
			t.Errorf("explicit startup budget was lost: got=%q want=300", got)
		}
		if ApprovedManagedEndpointHash(registration) == ApprovedManagedEndpointHash(baseline) {
			t.Error("changed startup budget reused the original approval hash")
		}
	})

	for _, test := range []struct {
		name, seconds string
	}{
		{"negative_budget_rejected", "-1"},
		{"overflowing_duration_rejected", "9223372036854775807"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registration, err := decode(test.seconds)
			if err != nil {
				return
			}
			if registration.Owner == nil {
				t.Fatal("invalid budget silently removed the approved owner")
			}
			if err := registration.Owner.Validate(); err == nil {
				t.Fatalf("invalid startup budget %s was accepted", test.seconds)
			}
		})
	}
}
