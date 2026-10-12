package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillAtomicWriteRejectsReplacedParentOutsideOpenedRoot(t *testing.T) {
	inside, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(inside, "references")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(inside)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := ensureSkillEditParent(root, "references"); err != nil {
		t.Fatal(err)
	}
	// Swap after the parent check. The opened root must still confine each
	// create, temporary write, rename and cleanup operation.
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	for _, exclusive := range []bool{true, false} {
		if err := writeSkillFileAtomic(root, "references/escape.md", []byte("escape"), exclusive); err == nil {
			t.Fatalf("escaped write accepted (exclusive=%v)", exclusive)
		}
	}
	if err := ensureSkillEditParent(root, "references/new"); err == nil {
		t.Fatal("escaped parent creation accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: entries=%v err=%v", entries, err)
	}
}
