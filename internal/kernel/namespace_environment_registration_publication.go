package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const namespaceRegistrationStagingPrefix = ".namespace-register-staging-"

// A final generation exists only after a complete verified marker has been
// synced in a private sibling staging directory. The no-replace rename also
// closes a concurrent destination race without deleting an unknown directory.
// The private writer dependency lets focused tests exercise real write errors;
// production uses the same atomic managed marker writer as every generation.
func publishNamespaceRegistrationGeneration(nameRoot string, marker managedEnvironmentMarker, writeMarker func(string, managedEnvironmentMarker) error) error {
	if !validSHA256(marker.Generation) || writeMarker == nil {
		return errors.New("environment registration publication identity is invalid")
	}
	resolved, err := filepath.EvalSymlinks(nameRoot)
	if err != nil || resolved != nameRoot {
		return errors.New("environment registration publication root is aliased")
	}
	final := filepath.Join(nameRoot, marker.Generation)
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		return errors.New("environment registration publication destination is already occupied")
	}
	staging, err := os.MkdirTemp(nameRoot, namespaceRegistrationStagingPrefix)
	if err != nil {
		return err
	}
	defer removeOwnedNamespaceRegistrationStaging(nameRoot, staging)
	if err := writeMarker(filepath.Join(staging, managedEnvironmentMarkerName), marker); err != nil {
		return err
	}
	verified, err := readManagedEnvironmentMarker(staging)
	if err != nil || !managedEnvironmentMarkerEquivalent(verified, marker) {
		return errors.New("environment registration staged marker verification failed")
	}
	if err := syncDirectory(staging); err != nil {
		return err
	}
	if err := renameNamespaceRegistrationExclusive(staging, final); err != nil {
		return err
	}
	return syncDirectory(nameRoot)
}

// Remove only this invocation's still-private directory. Interrupted staging
// from another invocation is never reclaimed here; it is non-authoritative
// and cannot occupy a final generation identity or block an idempotent retry.
func removeOwnedNamespaceRegistrationStaging(nameRoot, staging string) {
	if filepath.Dir(staging) != nameRoot || !strings.HasPrefix(filepath.Base(staging), namespaceRegistrationStagingPrefix) {
		return
	}
	info, err := os.Lstat(staging)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	resolved, err := filepath.EvalSymlinks(staging)
	if err != nil || resolved != staging {
		return
	}
	_ = os.RemoveAll(staging)
}
