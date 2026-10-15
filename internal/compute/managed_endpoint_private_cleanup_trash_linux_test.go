//go:build linux

package compute

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedEndpointPrivateCleanupValidatesScopedTrashBeforeReplay(t *testing.T) {
	for _, boundary := range []string{"mismatched_owner", "missing_owner_nonempty", "matching_owner", "empty_trash"} {
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
			key, err := scope.Key()
			if err != nil {
				t.Fatal(err)
			}
			trash := filepath.Join(root, ".retired-"+key)
			if err := os.Mkdir(trash, 0o700); err != nil {
				t.Fatal(err)
			}
			if boundary == "mismatched_owner" || boundary == "matching_owner" {
				descriptor := instance
				if boundary == "mismatched_owner" {
					descriptor.UserID = "different-owner"
				}
				if err := writeManagedEndpointPrivateJSON(filepath.Join(trash, "owner.json"), descriptor); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(trash, "output-0")
			if boundary != "empty_trash" {
				if err := os.WriteFile(output, []byte("private output"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, instance.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("source instance directory must be absent: %v", err)
			}
			err = CleanupManagedEndpointPrivateFiles(t.Context(), root, scope)
			if boundary == "mismatched_owner" || boundary == "missing_owner_nonempty" {
				if err == nil {
					t.Error("replay accepted unverified nonempty scoped trash")
				}
				raw, readErr := os.ReadFile(output)
				if readErr != nil || string(raw) != "private output" {
					t.Errorf("replay removed or changed unverified private output: %q %v", raw, readErr)
				}
				if boundary == "mismatched_owner" {
					if _, statErr := os.Stat(filepath.Join(trash, "owner.json")); statErr != nil {
						t.Errorf("replay removed mismatched owner evidence: %v", statErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("verified or empty trash should finish cleanup: %v", err)
			}
			if _, err := os.Lstat(trash); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("completed replay retained scoped trash: %v", err)
			}
		})
	}
}
