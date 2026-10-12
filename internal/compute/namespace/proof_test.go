package namespace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNamespaceProofRequiresActualFrozenOwnerBoundLookup(t *testing.T) {
	binding := JobBinding{JobID: "job", OwnerUserID: "owner", ProjectID: "project", Environment: "env", Provider: "provider", ExternalID: "actual-runtime", InputSHA256: strings.Repeat("a", 64), SourceSHA256: strings.Repeat("b", 64)}
	lookup := func(_ context.Context, owner, job string) (JobBinding, bool, error) {
		return binding, owner == binding.OwnerUserID && job == binding.JobID, nil
	}
	proof, err := Observe(context.Background(), binding, lookup)
	if err != nil || proof.VerifyInput(binding.InputSHA256) != nil {
		t.Fatal(err)
	}
	if proof.VerifyInput(strings.Repeat("c", 64)) == nil {
		t.Fatal("different workload input accepted")
	}
	changed := binding
	changed.Environment = "different"
	if _, err := Observe(context.Background(), changed, lookup); !errors.Is(err, ErrRecoveryUnconfirmed) {
		t.Fatal("different environment accepted")
	}
	changed = binding
	changed.OwnerUserID = "foreign"
	if _, err := Observe(context.Background(), changed, lookup); !errors.Is(err, ErrRecoveryUnconfirmed) {
		t.Fatal("foreign owner accepted")
	}
	if _, err := Observe(context.Background(), binding, func(context.Context, string, string) (JobBinding, bool, error) { return binding, false, nil }); !errors.Is(err, ErrRecoveryUnconfirmed) {
		t.Fatal("late unregistered historical job accepted")
	}
	if (Proof{}).VerifyInput(binding.InputSHA256) == nil {
		t.Fatal("zero proof accepted")
	}
	source := SourceBinding{JobID: binding.JobID, Environment: binding.Environment, Provider: binding.Provider, ExternalID: binding.ExternalID, ManifestSHA256: strings.Repeat("c", 64)}
	bound, err := proof.BindSource(source)
	if err != nil || bound.VerifyRead(binding.InputSHA256, source.ManifestSHA256) != nil {
		t.Fatal(err)
	}
	source.JobID = "same-input-different-job"
	if _, err := proof.BindSource(source); !errors.Is(err, ErrRecoveryUnconfirmed) {
		t.Fatal("source proof crossed same-input job boundary")
	}
}
