//go:build linux

package server

import (
	"os"
	"testing"
)

func TestOpenedGrantedDirectoryRejectsReplacedRootOutsideGrant(t *testing.T) {
	inside, outside := t.TempDir(), t.TempDir()
	grants := []hostGrant{{Path: inside, Mode: "read"}}
	root, err := os.OpenRoot(inside)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyOpenedGrantedDirectory(root, grants); err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	moved := inside + "-held"
	if err := os.Rename(inside, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(inside); _ = os.Rename(moved, inside) })
	if err := os.Symlink(outside, inside); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.OpenRoot(inside)
	if err != nil {
		t.Fatal(err)
	}
	defer replaced.Close()
	if err := verifyOpenedGrantedDirectory(replaced, grants); err == nil {
		t.Fatal("display path granted an opened outside directory")
	}
}
