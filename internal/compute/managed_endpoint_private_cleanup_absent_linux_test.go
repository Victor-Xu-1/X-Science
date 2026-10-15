//go:build linux

package compute

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedEndpointPrivateCleanupAcknowledgesAbsentRuntimeRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-owners")
	instance := managedEndpointPrivateCleanupRootTestInstance(t, root)
	scope, err := ManagedEndpointPrivateCleanupFor(instance)
	if err != nil {
		t.Fatal(err)
	}
	protected := []string{filepath.Join(instance.ServiceDir, "cache-entry"), instance.WorkspacePath}
	for _, path := range protected {
		if err := os.WriteFile(path, []byte("must remain"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime root must initially be absent: %v", err)
	}
	if err := CleanupManagedEndpointPrivateFiles(t.Context(), root, scope); err != nil {
		t.Fatalf("absent runtime root should need no cleanup: %v", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cleanup created an absent runtime root: %v", err)
	}
	for _, path := range protected {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != "must remain" {
			t.Errorf("cleanup touched a protected non-owner path %q: %q %v", path, raw, err)
		}
	}
}
