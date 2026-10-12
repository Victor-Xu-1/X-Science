package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestWorkspaceOpenNeverRenamesSiblingLegacyDatabaseWithoutHandoff(t *testing.T) {
	parent := t.TempDir()
	previous := filepath.Join(parent, "synonbiomed-v1.1.sqlite")
	legacy, err := sql.Open("sqlite", previous)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE handoff_sentinel(value TEXT); INSERT INTO handoff_sentinel VALUES('preserve')`); err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.Stat(previous + "-wal")
	if err != nil || walBefore.Size() == 0 {
		t.Fatalf("live historical WAL missing: %v", err)
	}
	blobRoot := previous + ".blobs"
	if err := os.Mkdir(blobRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(blobRoot, "preserved-object")
	if err := os.WriteFile(blob, []byte("preserved payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(parent, workspace.DatabaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := os.Stat(previous); err != nil {
		t.Fatalf("Open moved the sibling database without a verified handoff: %v", err)
	}
	walAfter, err := os.Stat(previous + "-wal")
	if err != nil || !os.SameFile(walBefore, walAfter) {
		t.Fatalf("Open replaced the sibling WAL: %v", err)
	}
	var value string
	if err := legacy.QueryRow(`SELECT value FROM handoff_sentinel`).Scan(&value); err != nil || value != "preserve" {
		t.Fatalf("historical live store changed: %q %v", value, err)
	}
	if raw, err := os.ReadFile(blob); err != nil || string(raw) != "preserved payload" {
		t.Fatalf("historical blob changed: %q %v", raw, err)
	}
}
