package server

import (
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/sciencecapability"
)

func TestPublishedReceiptCommandRetainsOnlyExactSingleEntrypoint(t *testing.T) {
	pack := sciencecapability.ExecutionPack{ID: "generic.engine", Mode: "local", Skill: "managed-workflow", Executable: "python", Script: "executionpacks/run.py"}
	valid := "python /workspace/.synon/runtime/skills/managed-workflow-id/scripts/run.py --out results"
	if !managedExecutionReceiptCommandMatches(pack.Skill, pack, valid) {
		t.Fatal("published receipt command was not recognized")
	}
	if commandExecutesManagedExecutionPack(pack.Skill, pack, valid) {
		t.Fatal("read compatibility granted new execution authority")
	}
	for _, command := range []string{valid + " > report.md", valid + "; printf altered", "python -c 'altered' /workspace/.synon/runtime/skills/managed-workflow-id/scripts/run.py", "python /workspace/.synon/runtime/skills/other-id/scripts/run.py"} {
		if managedExecutionReceiptCommandMatches(pack.Skill, pack, command) {
			t.Fatalf("decorated/foreign receipt command was accepted: %s", command)
		}
	}
}

func TestUnavailableOwnershipReservesLogicalAndPhysicalNamespaces(t *testing.T) {
	root := t.TempDir()
	authority := managedExecutionOutputAuthority{Root: filepath.Join(root, "results"), ResolvedRoot: filepath.Join(root, ".synon-artifacts", ".managed", "receipt"), Unavailable: true}
	for _, path := range []string{filepath.Join(authority.Root, "report.csv"), filepath.Join(authority.ResolvedRoot, "report.csv")} {
		if !managedExecutionAuthorityContainsPath(authority, path) {
			t.Fatalf("lost content revoked reserved output namespace: %s", path)
		}
	}
	if managedExecutionAuthorityContainsPath(authority, filepath.Join(root, "unrelated.md")) {
		t.Fatal("unrelated file was covered by output ownership")
	}
}

func TestManagedMetadataRejectsUnknownSchemasAmbiguityAndEscapingLinks(t *testing.T) {
	root := t.TempDir()
	name := publishedManagedExecutionOwnerMarker
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(`{"schema":"unknown","execution_pack_id":"generic.engine"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, state := inspectManagedExecutionOutputOwnership(root); state != managedExecutionOutputOwnershipInvalid {
		t.Fatal("unknown owner schema was accepted")
	}
	valid := []byte(`{"schema":"synon.execution-pack-output-owner.v1","execution_pack_id":"generic.engine"}`)
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, state := inspectManagedExecutionOutputOwnership(root); state != managedExecutionOutputOwnershipValid {
		t.Fatal("published schema was rejected")
	}
	if err := os.WriteFile(filepath.Join(root, managedExecutionOutputOwnershipMarker), []byte(`{"schema":"x-science.execution-pack-output-owner.v1","execution_pack_id":"generic.engine"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, state := inspectManagedExecutionOutputOwnership(root); state != managedExecutionOutputOwnershipInvalid {
		t.Fatal("ambiguous ownership markers were accepted")
	}
	outside := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(outside, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), name)
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlink fixture unsupported: %v", err)
	}
	if _, err := readManagedExecutionMetadata(filepath.Dir(linked), name); err == nil {
		t.Fatal("foreign marker link was read")
	}
}
