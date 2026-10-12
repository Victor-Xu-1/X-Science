package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestOpenWorkspaceStoreUsesXScienceHome(t *testing.T) {
	home := t.TempDir()
	store, err := openWorkspaceStore(home)
	if err != nil {
		t.Fatalf("open workspace store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if want := filepath.Join(home, filepath.FromSlash(workspace.DatabaseRelativePath)); !fileExists(want) {
		t.Fatalf("workspace database was not created at %s", want)
	}
	if _, err := store.TranscriptRepository(context.Background()); err != nil {
		t.Fatalf("workspace transcript authority: %v", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
