package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	workspace "x-science/internal/persistence/workspace"
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

func TestWorkspaceStartupReadsPublishedDatabaseWithoutCopyOrDeletion(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "workspace", workspace.PublishedDatabaseFilename)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	previous, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := previous.Exec(`CREATE TABLE retained_startup(value TEXT); INSERT INTO retained_startup VALUES('retained')`); err != nil {
		t.Fatal(err)
	}
	if err := previous.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := openWorkspaceStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var value string
	if err := reader.QueryRowContext(context.Background(), `SELECT value FROM retained_startup`).Scan(&value); err != nil || value != "retained" {
		t.Fatalf("published data was not opened: %q %v", value, err)
	}
	var schema int
	if err := reader.QueryRow(`SELECT MAX(version) FROM workspace_schema_migrations`).Scan(&schema); err != nil || schema != 75 {
		t.Fatalf("original database was not opened/upgraded: %d %v", schema, err)
	}
	if _, err := os.Stat(filepath.Join(home, workspace.DatabaseRelativePath)); !os.IsNotExist(err) {
		t.Fatal("startup created a second empty database authority")
	}
	if _, err := os.Stat(path + ".blobs"); err != nil {
		t.Fatal("published blob pair was lost")
	}
}
