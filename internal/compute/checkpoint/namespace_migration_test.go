package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"x-science/internal/compute/namespace"
)

func checkpointNamespaceProof(t *testing.T, input, manifestSHA string) namespace.SourceProof {
	t.Helper()
	binding := namespace.JobBinding{JobID: "old-job", OwnerUserID: "owner", ProjectID: "project", Environment: "env", Provider: "ssh", ExternalID: "actual-remote-job", InputSHA256: input, SourceSHA256: strings.Repeat("b", 64)}
	proof, err := namespace.Observe(context.Background(), binding, func(context.Context, string, string) (namespace.JobBinding, bool, error) { return binding, true, nil })
	if err != nil {
		t.Fatal(err)
	}
	source, err := proof.BindSource(namespace.SourceBinding{JobID: binding.JobID, Environment: binding.Environment, Provider: binding.Provider, ExternalID: binding.ExternalID, ManifestSHA256: manifestSHA})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestNamespaceCheckpointHistoricalReadRequiresCompleteSourceProof(t *testing.T) {
	root, contract, input, raw := checkpointFixture(t)
	previous := bytes.Replace(raw, []byte(Schema), []byte(previousNamespaceSchema), 1)
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(previous))
	if _, err := Read(context.Background(), bytes.NewReader(previous), input, contract.CommandSHA256(), nil); err == nil {
		t.Fatal("ordinary new job accepted old schema")
	}
	count := 0
	proof := checkpointNamespaceProof(t, input, manifestSHA)
	receipt, err := ReadWithHistoricalProof(context.Background(), bytes.NewReader(previous), input, contract.CommandSHA256(), manifestSHA, func(File) error { count++; return nil }, proof)
	if err != nil || count != 1 || receipt.Schema != previousNamespaceSchema || receipt.ManifestSHA256 != manifestSHA {
		t.Fatalf("historical source was not preserved: %#v %v", receipt, err)
	}
	if err := os.WriteFile(filepath.Join(root, contract.Manifest), previous, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWithHistoricalProof(context.Background(), root, t.TempDir(), contract, input, manifestSHA, proof); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out/state.bin"), []byte("tampered native state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWithHistoricalProof(context.Background(), root, t.TempDir(), contract, input, manifestSHA, proof); err == nil {
		t.Fatal("old namespace bypassed payload verification")
	}
}

func TestNamespaceCheckpointUnknownSourceNeverInvokesFileConsumer(t *testing.T) {
	_, contract, input, raw := checkpointFixture(t)
	previous := bytes.Replace(raw, []byte(Schema), []byte(previousNamespaceSchema), 1)
	proof := checkpointNamespaceProof(t, input, fmt.Sprintf("%x", sha256.Sum256(previous)))
	for _, test := range []struct {
		proof namespace.SourceProof
		sha   string
	}{{namespace.SourceProof{}, fmt.Sprintf("%x", sha256.Sum256(previous))}, {proof, strings.Repeat("0", 64)}} {
		count := 0
		if _, err := ReadWithHistoricalProof(context.Background(), bytes.NewReader(previous), input, contract.CommandSHA256(), test.sha, func(File) error { count++; return nil }, test.proof); err == nil || count != 0 {
			t.Fatal("unconfirmed old source invoked consumer")
		}
	}
}
