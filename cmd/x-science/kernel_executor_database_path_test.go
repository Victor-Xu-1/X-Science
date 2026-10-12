package main

import (
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestKernelExecutorDatabasePathUsesStartupAuthority(t *testing.T) {
	home := t.TempDir()
	published := filepath.Join(home, "workspace", workspace.PublishedDatabaseFilename)
	if err := os.MkdirAll(filepath.Dir(published), 0o700); err != nil {
		t.Fatal(err)
	}
	primary, err := workspace.Open(published)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}

	canonical := filepath.Join(home, "workspace", workspace.DatabaseFilename)
	if _, err := workspace.OpenExisting(canonical); err == nil {
		t.Fatal("kernel executor must not assume the canonical filename when only the published authority exists")
	}

	path, err := workspace.StartupDatabasePath(home)
	if err != nil || path != published {
		t.Fatalf("startup database path=%q err=%v", path, err)
	}
	secondary, err := workspace.OpenExisting(path)
	if err != nil {
		t.Fatalf("kernel executor startup path must open the retained published authority: %v", err)
	}
	if err := secondary.Close(); err != nil {
		t.Fatal(err)
	}
}
