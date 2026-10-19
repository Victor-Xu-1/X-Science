package server

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestKernelSkillPackageReadUsesHeldRootAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	selected := filepath.Join(parent, "selected")
	writePackage := func(root, content, resource string) {
		t.Helper()
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		for path, body := range map[string]string{"SKILL.md": content, resource: "Resource."} {
			if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePackage(selected, "Original package.", "original.txt")
	directory, err := os.OpenRoot(selected)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.Rename(selected, filepath.Join(parent, "retained-original")); err != nil {
		t.Skipf("opened-directory rename unavailable: %v", err)
	}
	writePackage(selected, "Replacement package.", "replacement.txt")
	content, files, err := readKernelSkillPackage(directory, "SKILL.md")
	if err != nil || content != "Original package." || !reflect.DeepEqual(files, []string{"SKILL.md", "original.txt"}) {
		t.Fatalf("read reopened a path instead of borrowing one held root: content=%q files=%#v err=%v", content, files, err)
	}
}

func TestKernelSkillPackageReadRejectsSpecialFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("Repairable draft."), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, "entry.sock"))
	if err != nil {
		t.Skipf("local socket fixture unavailable: %v", err)
	}
	defer listener.Close()
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	for _, requested := range []string{"SKILL.md", "entry.sock"} {
		content, files, err := readKernelSkillPackage(directory, requested)
		if err == nil || content != "" || files != nil {
			t.Fatalf("special resource yielded partial content or inventory: content=%q files=%#v err=%v", content, files, err)
		}
	}
}
