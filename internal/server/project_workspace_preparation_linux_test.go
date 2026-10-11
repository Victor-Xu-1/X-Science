package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedProjectWorkspacePinsUnlinkedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent", "project")
	resource, err := prepareManagedProjectWorkspace(path)
	if err != nil || resource.Rollback == nil {
		t.Fatalf("prepare: rollback=%t err=%v", resource.Rollback != nil, err)
	}
	defer resource.Release()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && target == path+" (deleted)" {
			pinned = true
		}
	}
	if !pinned {
		t.Fatal("prepared directory inode is not held through transaction outcome")
	}
	if err := resource.Rollback(); err == nil {
		t.Fatal("unlink/recreate replacement directory removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement directory lost: %v", err)
	}
}
