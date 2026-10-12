package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalDirectoryCreationRejectsLinkedParentBeforeWriting(t *testing.T) {
	inside, outside := t.TempDir(), t.TempDir()
	linked := filepath.Join(inside, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalOrCreateDirectory(filepath.Join(linked, "not-authorized", "nested")); err == nil {
		t.Fatal("linked parent accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "not-authorized")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed directory admission wrote outside its parent: %v", err)
	}
}

func TestCreatedDirectoryCleanupPreservesReplacementLeaf(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	owned, err := openCanonicalDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	if err := os.Rename(path, path+"-held"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedEmptyDirectory(path, owned); err == nil {
		t.Fatal("replacement directory removed by old operation")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("replacement not preserved: %v", err)
	}
}
