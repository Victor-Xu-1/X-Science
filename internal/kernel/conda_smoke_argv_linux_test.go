//go:build linux

package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedPythonSmokePassesPrefixAsDataArgument(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "quote' double\" backslash\\ directory")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	// An actual child process checks argv boundaries without installing or
	// running scientific software. Dynamic paths must never enter Python code.
	script := `#!/bin/sh
set -eu
[ "$#" -eq 4 ]
[ "$1" = '-I' ]
[ "$2" = '-c' ]
[ "$4" = "$CONDA_PREFIX" ]
case "$3" in *'dir=sys.argv[1]'*) ;; *) exit 2 ;; esac
case "$3" in *"$CONDA_PREFIX"*) exit 3 ;; esac
printf '%s\n' '{"ok":true,"rdkit":"fixture"}'
`
	if err := os.WriteFile(filepath.Join(prefix, "bin", "python"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Config{})
	if err := m.smokeManagedPython(context.Background(), managedPythonRuntime{rdkitVersion: "fixture"}, prefix); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prefix, "'") {
		t.Fatal("quoting fixture lost its special character")
	}
}
