package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceStartupPreservesLiveHistoricalHomeUntilVerifiedHandoff(t *testing.T) {
	parent := t.TempDir()
	legacyHome := filepath.Join(parent, ".synon-go") // Published installation fact, not current branding.
	legacyDirectory := filepath.Join(legacyHome, "workspace")
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDirectory, "synonbiomed-v1.1.sqlite")
	database, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE startup_boundary(value TEXT); INSERT INTO startup_boundary VALUES('live')`); err != nil {
		t.Fatal(err)
	}
	blobRoot := legacyPath + ".blobs"
	if err := os.Mkdir(blobRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(blobRoot, "retained.bin")
	if err := os.WriteFile(blob, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openWorkspaceStore(filepath.Join(parent, ".x-science"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, path := range []string{legacyPath, legacyPath + "-wal", blob} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("startup changed a live historical source without verified handoff: %s: %v", path, err)
		}
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM startup_boundary`).Scan(&value); err != nil || value != "live" {
		t.Fatalf("original open database lost continuity: value=%q err=%v", value, err)
	}
}
