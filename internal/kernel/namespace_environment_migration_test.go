package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func previousNamespaceFixture(t *testing.T) (*Manager, string, managedEnvironmentMarker, []byte) {
	t.Helper()
	root := t.TempDir()
	marker := managedEnvironmentMarker{SchemaVersion: managedEnvironmentMarkerVersion, ValidationRevision: managedEnvironmentValidationRevision,
		Name: "user-scientific-env", Language: "python", Kind: "conda", Packages: []string{"python=3.11", "numpy=2.1"},
		Operation: "create", OperationKey: strings.Repeat("a", 64)}
	marker.Generation = previousNamespaceEnvironmentGeneration(marker)
	prefix := filepath.Join(root, ".generations", marker.Name, marker.Generation)
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, "immutable-payload"), []byte("original scientific payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(prefix, filepath.Join(root, marker.Name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return NewManager(Config{CondaEnvsPath: root}), prefix, marker, raw
}

func TestNamespaceEnvironmentUpgradePreservesImmutableGenerationAndSource(t *testing.T) {
	m, prefix, original, raw := previousNamespaceFixture(t)
	if err := m.UpgradeManagedEnvironmentNamespace(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker, err := readManagedEnvironmentMarker(prefix)
	if err != nil || marker.Generation != original.Generation || marker.OperationKey != original.OperationKey || marker.PreviousNamespace == nil {
		t.Fatalf("migration changed immutable execution identity: marker=%+v err=%v", marker, err)
	}
	if marker.PreviousNamespace.MarkerJSON != string(raw) {
		t.Fatal("original marker bytes were not preserved")
	}
	if _, err := os.Stat(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName)); !os.IsNotExist(err) {
		t.Fatal("old marker was not retired after publication")
	}
	target, err := filepath.EvalSymlinks(filepath.Join(m.config.CondaEnvsPath, original.Name))
	if err != nil || target != prefix {
		t.Fatalf("active prefix changed: %s %v", target, err)
	}
	if contents, err := os.ReadFile(filepath.Join(prefix, "immutable-payload")); err != nil || string(contents) != "original scientific payload" {
		t.Fatal("environment payload changed")
	}
	if err := m.UpgradeManagedEnvironmentNamespace(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceEnvironmentCorruptCurrentMarkerNeverFallsBack(t *testing.T) {
	_, prefix, _, _ := previousNamespaceFixture(t)
	if err := os.WriteFile(filepath.Join(prefix, managedEnvironmentMarkerName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManagedEnvironmentMarker(prefix); err == nil {
		t.Fatal("corrupt canonical marker fell back to historical data")
	}
}

func TestNamespaceEnvironmentUpgradeRejectsChangedGenerationAndKeepsSource(t *testing.T) {
	m, prefix, marker, _ := previousNamespaceFixture(t)
	marker.Packages = []string{"python=3.12"}
	raw, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.UpgradeManagedEnvironmentNamespace(context.Background()); err == nil {
		t.Fatal("changed package generation accepted")
	}
	if _, err := os.Stat(filepath.Join(prefix, previousNamespaceEnvironmentMarkerName)); err != nil {
		t.Fatal("recovery source was removed")
	}
}

func TestNamespaceEnvironmentSourceProofCannotChangeCurrentFields(t *testing.T) {
	_, prefix, _, _ := previousNamespaceFixture(t)
	marker, err := readManagedEnvironmentMarker(prefix)
	if err != nil {
		t.Fatal(err)
	}
	marker.ImportNames = []string{"different_module"}
	if _, err := validateManagedEnvironmentMarker(marker); err == nil {
		t.Fatal("source proof authorized changed witness fields")
	}
	marker, _ = readManagedEnvironmentMarker(prefix)
	marker.PreviousNamespace.SHA256 = strings.Repeat("0", 64)
	if _, err := validateManagedEnvironmentMarker(marker); err == nil {
		t.Fatal("corrupt source digest accepted")
	}
}

func TestNamespaceEnvironmentOldCreateReceiptMatchesOnlyExactDefinition(t *testing.T) {
	definition := struct {
		Name, Language, SourceEnvironment string
		Packages                          []string
	}{"custom-env", "python", defaultManagedPythonEnvironment, []string{"synon-user-package"}}
	previous := definition
	previous.SourceEnvironment = "synon-biomed-python"
	raw, _ := json.Marshal(previous)
	digest := sha256.Sum256(append([]byte("synon-managed-environment-operation-v1\x00create\x00"), raw...))
	marker := managedEnvironmentMarker{OperationKey: hex.EncodeToString(digest[:]), PreviousNamespace: &managedEnvironmentNamespaceSource{}}
	ctx := previousNamespaceCreateContext(context.Background(), definition)
	if !namespaceOperationKeyMatches(ctx, marker, managedEnvironmentOperationKey("create", definition)) {
		t.Fatal("exact existing create receipt was not recognized")
	}
	definition.Packages = []string{"changed-package"}
	if namespaceOperationKeyMatches(previousNamespaceCreateContext(context.Background(), definition), marker, managedEnvironmentOperationKey("create", definition)) {
		t.Fatal("different request reused old side effects")
	}
	marker.PreviousNamespace = nil
	if namespaceOperationKeyMatches(ctx, marker, managedEnvironmentOperationKey("create", previous)) {
		t.Fatal("unproven old namespace key accepted")
	}
}
