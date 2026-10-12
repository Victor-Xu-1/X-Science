package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/datadir"
)

func TestNamespaceDefaultDataPointerIsHonoredBeforeRuntimeOpen(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("X_SCIENCE_DATA_DIR_CONTROL", "")
	previous := filepath.Join(root, "synon-go", "data-dir.json")
	chosen := filepath.Join(root, "chosen-data")
	for _, path := range []string{filepath.Dir(previous), chosen} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(datadir.State{SchemaVersion: 1, Current: chosen})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{HomeDir: filepath.Join(root, "default")}
	if err := applyDataDirectoryControl(&cfg, ""); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "x-science", "data-dir.json")
	if cfg.HomeDir != chosen || cfg.DataDirSource != "pointer" || cfg.DataDirControlPath != current {
		t.Fatalf("saved data-directory selection was ignored: %+v", cfg)
	}
	actual, err := os.ReadFile(current)
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatal("default pointer was reformatted during namespace transfer")
	}
}

func TestNamespaceExplicitDataControlRemainsOperatorOwned(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	custom := filepath.Join(root, "custom", "operator-pointer.json")
	t.Setenv("X_SCIENCE_DATA_DIR_CONTROL", custom)
	actual, err := dataDirectoryControlPath()
	if err != nil || actual != custom {
		t.Fatal("custom operator path was changed")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatal("custom path caused an unrelated default pointer upgrade")
	}
}
