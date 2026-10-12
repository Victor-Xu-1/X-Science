package workspace

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishCanonicalWorkspaceDatabaseDrainsLegacyWALAndBlobs(t *testing.T) {
	legacyHome := filepath.Join(t.TempDir(), ".synon-go")
	workspaceDir := filepath.Join(legacyHome, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(workspaceDir, legacyWorkspaceDatabaseFilename)
	legacyDB, err := sql.Open(sqliteDriver, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE publication_sentinel(value TEXT); INSERT INTO publication_sentinel VALUES('keep')`); err != nil {
		_ = legacyDB.Close()
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		_ = legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}
	blobRoot := legacyPath + ".blobs"
	if err := os.Mkdir(blobRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(blobRoot, "artifact.bin")
	if err := os.WriteFile(blob, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	canonicalHome := filepath.Join(filepath.Dir(legacyHome), ".x-science")
	canonicalPath := filepath.Join(canonicalHome, "workspace", DatabaseFilename)
	if err := PublishCanonicalWorkspaceDatabase(canonicalPath, []string{canonicalHome, legacyHome}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy database remained: %v", err)
	}
	if _, err := os.Stat(legacyPath + "-wal"); !os.IsNotExist(err) {
		t.Fatal("legacy WAL remained after publication")
	}
	canonicalDB, err := sql.Open(sqliteDriver, canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer canonicalDB.Close()
	var value string
	if err := canonicalDB.QueryRow(`SELECT value FROM publication_sentinel`).Scan(&value); err != nil || value != "keep" {
		t.Fatalf("published database sentinel=%q err=%v", value, err)
	}
	if raw, err := os.ReadFile(filepath.Join(canonicalPath+".blobs", "artifact.bin")); err != nil || string(raw) != "payload" {
		t.Fatalf("published blob=%q err=%v", raw, err)
	}
}

func TestPublishCanonicalWorkspaceDatabaseLeavesSiblingLegacyWithoutHandoff(t *testing.T) {
	parent := t.TempDir()
	legacyPath := filepath.Join(parent, legacyWorkspaceDatabaseFilename)
	legacyDB, err := sql.Open(sqliteDriver, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE handoff_sentinel(value TEXT); INSERT INTO handoff_sentinel VALUES('preserve')`); err != nil {
		_ = legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}
	canonicalPath := filepath.Join(parent, DatabaseFilename)
	if err := PublishCanonicalWorkspaceDatabase(canonicalPath, []string{parent}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy database was renamed without an explicit workspace home layout: %v", err)
	}
}
