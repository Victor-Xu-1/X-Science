package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/kernel"
)

func TestEnvironmentNamespaceCLIInspectIsReadOnlyAndRedacted(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	if err := runEnvironmentNamespaceCLI(context.Background(), []string{"inspect", "--source-root", root}, &output); err != nil {
		t.Fatal(err)
	}
	var plan environmentNamespacePlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil || plan.SchemaVersion != 1 || len(plan.SourceSHA256) != 64 || plan.Applied != 0 || len(plan.Environments) != 0 {
		t.Fatalf("invalid inspect result: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 || strings.Contains(output.String(), root) {
		t.Fatal("inspect mutated or exposed a source path")
	}
	imports := []kernel.NamespaceEnvironmentImport{{CurrentName: "current", SourceName: "private-name", SourcePrefix: "/private/prefix", Packages: []string{"private-package"}, SourceSHA256: strings.Repeat("a", 64), Generation: strings.Repeat("b", 64), NameOrigin: "user-resource"}}
	redacted, err := planEnvironmentNamespace(imports)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(redacted)
	for _, private := range []string{"private-name", "/private/prefix", "private-package"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("namespace plan leaked source details")
		}
	}
}

func TestEnvironmentNamespaceCLIRejectsWritesWithoutFrozenSource(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "untouched")
	for _, args := range [][]string{
		{}, {"apply"}, {"inspect", "--source-root", source, "--target-root", target},
		{"apply", "--source-root", source, "--target-root", target, "--expected-source-sha256", strings.Repeat("a", 64)},
	} {
		var output bytes.Buffer
		if err := runEnvironmentNamespaceCLI(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatal("unbound command applied or emitted a success result")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatal("unbound command changed target registry")
		}
	}
}

func TestEnvironmentNamespaceCLIRejectsOverlappingRoots(t *testing.T) {
	source := t.TempDir()
	for _, target := range []string{source, filepath.Join(source, "nested"), filepath.Dir(source)} {
		var output bytes.Buffer
		if err := runEnvironmentNamespaceCLI(context.Background(), []string{"apply", "--source-root", source, "--target-root", target, "--expected-source-sha256", strings.Repeat("a", 64)}, &output); err == nil || output.Len() != 0 {
			t.Fatal("overlapping root command was admitted")
		}
	}
	if entries, err := os.ReadDir(source); err != nil || len(entries) != 0 {
		t.Fatal("overlapping root command changed source")
	}
}
