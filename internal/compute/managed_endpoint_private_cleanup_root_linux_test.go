//go:build linux

package compute

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedEndpointPrivateCleanupRejectsUntrustedRuntimeRoot(t *testing.T) {
	for _, boundary := range []string{"runtime_root_symlink", "nonprivate_runtime_root"} {
		t.Run(boundary, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "owners")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			instance := managedEndpointPrivateCleanupRootTestInstance(t, root)
			scope, err := ManagedEndpointPrivateCleanupFor(instance)
			if err != nil {
				t.Fatal(err)
			}
			writeTree := func(directory string) string {
				t.Helper()
				owner := filepath.Join(directory, instance.ID)
				if err := os.MkdirAll(owner, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := writeManagedEndpointPrivateJSON(filepath.Join(owner, "owner.json"), instance); err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(owner, "output-0")
				if err := os.WriteFile(output, []byte("private output must remain"), 0o600); err != nil {
					t.Fatal(err)
				}
				return output
			}
			protected := []string{writeTree(root)}
			if boundary == "runtime_root_symlink" {
				original := root + ".original"
				if err := os.Rename(root, original); err != nil {
					t.Fatal(err)
				}
				protected[0] = filepath.Join(original, instance.ID, "output-0")
				outside := t.TempDir()
				protected = append(protected, writeTree(outside))
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := CleanupManagedEndpointPrivateFiles(t.Context(), root, scope); err == nil {
				t.Error("private cleanup accepted an untrusted runtime root")
			}
			for _, path := range protected {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "private output must remain" {
					t.Errorf("untrusted runtime root deleted or changed protected output %q: %q %v", path, raw, err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(path), "owner.json")); err != nil {
					t.Errorf("untrusted runtime root removed owner evidence: %v", err)
				}
			}
		})
	}
}

func managedEndpointPrivateCleanupRootTestInstance(t *testing.T, root string) ManagedEndpointOwnerInstance {
	t.Helper()
	registration := ManagedEndpointRegistration{Name: "private-root-owner", URL: "http://127.0.0.1:25001", Port: 25001,
		LivePath: "/health", StartScript: "not executed", StopScript: "not executed",
		Owner: &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	instance := ManagedEndpointOwnerInstance{ID: "private-root-instance", UserID: "owner-a", Endpoint: registration.Name,
		Registration: registration, ApprovedHash: ApprovedManagedEndpointHash(registration), StartGeneration: 1,
		RuntimeRoot: root, ServiceDir: t.TempDir(), WorkspacePath: filepath.Join(t.TempDir(), "workspace.db"),
		InitialAction: ManagedEndpointActionRef{InstanceID: "private-root-instance", Kind: "start", ClaimHandle: "original", Generation: 1}}
	if err := instance.Validate(); err != nil {
		t.Fatal(err)
	}
	return instance
}
