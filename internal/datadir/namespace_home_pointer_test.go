package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradeLegacyDefaultHomePointerRewritesOriginalDefaultHome(t *testing.T) {
	root := t.TempDir()
	controlPath := filepath.Join(root, "config", "x-science", "data-dir.json")
	legacyHome := filepath.Join(root, "homes", "synon-go")
	canonicalHome := filepath.Join(root, "homes", "x-science")
	if err := os.MkdirAll(filepath.Dir(controlPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(canonicalHome, 0o700); err != nil {
		t.Fatal(err)
	}
	writeNamespaceState(t, controlPath, State{SchemaVersion: 1, Current: legacyHome})
	if err := UpgradeLegacyDefaultHomePointer(controlPath, legacyHome, canonicalHome); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(controlPath).Load()
	if err != nil || loaded.Current != canonicalHome {
		t.Fatalf("rewritten pointer=%+v err=%v", loaded, err)
	}
}

func TestUpgradeLegacyDefaultHomePointerPreservesCustomHome(t *testing.T) {
	root := t.TempDir()
	controlPath := filepath.Join(root, "config", "x-science", "data-dir.json")
	customHome := filepath.Join(root, "homes", "operator-data")
	legacyHome := filepath.Join(root, "homes", "synon-go")
	canonicalHome := filepath.Join(root, "homes", "x-science")
	if err := os.MkdirAll(filepath.Dir(controlPath), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{customHome, legacyHome, canonicalHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeNamespaceState(t, controlPath, State{SchemaVersion: 1, Current: customHome})
	if err := UpgradeLegacyDefaultHomePointer(controlPath, legacyHome, canonicalHome); err != nil {
		t.Fatal(err)
	}
	loaded, err := New(controlPath).Load()
	if err != nil || loaded.Current != customHome {
		t.Fatalf("custom pointer changed: %+v err=%v", loaded, err)
	}
}
