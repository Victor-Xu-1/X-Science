package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetImportProvenanceDoesNotRelaxCurrentVerification(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{SchemaVersion: 1, Provenance: &ImportedProvenance{
		OriginalComponent: "original component", OriginalGitRevision: strings.Repeat("a", 40), OriginalArchiveSHA256: strings.Repeat("b", 64),
	}}
	if _, err := Verify(root, manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.OriginalArchiveSHA256 = "invalid"
	if _, err := Verify(root, manifest); err == nil {
		t.Fatal("invalid provenance hash accepted")
	}
	manifest.Provenance.OriginalArchiveSHA256 = strings.Repeat("b", 64)
	manifest.Files = []File{{Path: "../outside", SHA256: strings.Repeat("c", 64)}}
	if _, err := Verify(root, manifest); err == nil {
		t.Fatal("source provenance bypassed contained asset validation")
	}
	path := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"provenance":{"originalComponent":"source","unknown":"not allowed"},"files":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown nested provenance field accepted")
	}
}
