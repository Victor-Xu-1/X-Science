package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestOpenWorkspaceStorePublishesLegacyDefaultHomeDatabase(t *testing.T) {
	root := t.TempDir()
	legacyHome := filepath.Join(root, ".synon-go")
	workspaceDir := filepath.Join(legacyHome, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(workspaceDir, "synonbiomed-v1.1.sqlite")
	legacyDB, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`CREATE TABLE startup_sentinel(value TEXT); INSERT INTO startup_sentinel VALUES('legacy')`); err != nil {
		_ = legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	canonicalHome := filepath.Join(root, ".x-science")
	store, err := openWorkspaceStore(canonicalHome)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	canonicalPath := filepath.Join(canonicalHome, "workspace", workspace.DatabaseFilename)
	if _, err := os.Stat(canonicalPath); err != nil {
		t.Fatalf("canonical workspace database missing: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy database remained: %v", err)
	}
	if _, err := store.TranscriptRepository(context.Background()); err != nil {
		t.Fatalf("workspace transcript authority: %v", err)
	}
}
