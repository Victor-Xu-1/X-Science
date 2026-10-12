package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromRootRetainsAuthorizedTreeAfterDisplayPathReplacement(t *testing.T) {
	directory, outside := t.TempDir(), t.TempDir()
	write := func(path, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: fixture\n---\nActual body."), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(directory, "authorized-source")
	write(outside, "outside-private-source")
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := directory + "-held"
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(directory); _ = os.Rename(moved, directory) })
	if err := os.Symlink(outside, directory); err != nil {
		t.Fatal(err)
	}
	catalog := LoadFromRoot(root, directory)
	items := catalog.Skills()
	if len(items) != 1 || items[0].Name != "authorized-source" {
		t.Fatalf("load selected replaced display path: items=%v errors=%v", items, catalog.LoadErrors())
	}
}
