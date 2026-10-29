//go:build linux && amd64

package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxSessionRuntimeMountsPreserveOnlyResolvedExternalPython(t *testing.T) {
	root := t.TempDir()
	stdlib := filepath.Join(root, "lib", "python3.11", "encodings")
	if err := os.MkdirAll(stdlib, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stdlib, "__init__.py"), []byte("# fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(root, "bin", "python3.11")
	mounts, err := platformSessionRuntimeMounts(python, t.TempDir(), "/private/worker.py", t.TempDir())
	if err != nil || len(mounts) != 1 || mounts[0].Path != root || mounts[0].Writable || !mounts[0].trusted || mounts[0].regular {
		t.Fatalf("resolved supervisor did not retain its sole read-only runtime grant: %#v %v", mounts, err)
	}
	for _, executable := range []string{"/usr/bin/python3", "/bin/bash", filepath.Join(root, "bin", "Rscript")} {
		mounts, err := platformSessionRuntimeMounts(executable, root, "/private/worker.py", root)
		if err != nil || len(mounts) != 0 {
			t.Fatalf("unrelated prefix or worker argument extended authority for %s: %#v %v", executable, mounts, err)
		}
	}
}
