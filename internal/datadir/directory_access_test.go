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
