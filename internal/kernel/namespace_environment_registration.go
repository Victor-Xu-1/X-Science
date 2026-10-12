package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// AdoptManagedEnvironmentNamespaceImport publishes only a canonical registry
// record. It never moves/copies/reinstalls the source environment or rewrites
// its original activation/marker. Callers must already own a drained migration
// boundary; this administrator API is not exposed as an agent execution tool.
func (m *Manager) AdoptManagedEnvironmentNamespaceImport(ctx context.Context, frozen NamespaceEnvironmentImport) (ManagedEnvironment, error) {
	root, current, marker, err := m.prepareNamespaceEnvironmentImport(ctx, frozen)
	if err != nil {
		return ManagedEnvironment{}, err
	}
	nameRoot := filepath.Join(root, ".generations", marker.Name)
	if err := validateNamespaceRegistryGenerationRoot(root, marker.Name); err != nil {
		return ManagedEnvironment{}, err
	}
	if err := os.MkdirAll(nameRoot, 0o700); err != nil {
		return ManagedEnvironment{}, err
	}
	resolved, err := filepath.EvalSymlinks(nameRoot)
	if err != nil || resolved != nameRoot {
		return ManagedEnvironment{}, errors.New("environment adoption registry is aliased")
	}
	release, err := lockKernelFile(ctx, filepath.Join(nameRoot, ".install.lock"))
	if err != nil {
		return ManagedEnvironment{}, err
	}
	defer release()
	active := filepath.Join(root, marker.Name)
	if _, err := os.Lstat(active); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(active)
		existing, readErr := m.activeManagedEnvironmentMarker(marker.Name)
		if resolveErr != nil || resolved != filepath.Join(nameRoot, marker.Generation) || readErr != nil || !managedEnvironmentMarkerEquivalent(existing, marker) {
			return ManagedEnvironment{}, errors.New("environment adoption would overwrite an existing logical environment")
		}
		return m.readManagedEnvironment(marker.Name, true)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ManagedEnvironment{}, err
	}
	// Observe real runtime health using the existing manager contract; no
	// installation recipe revision is promoted merely by a namespace change.
	if err := m.verifyNamespaceRegisteredEnvironmentHealth(ctx, marker); err != nil {
		return ManagedEnvironment{}, err
	}
	verified, err := inspectNamespaceEnvironmentSource(ctx, current.source.SourceRoot, current.SourceName)
	if err != nil || !reflect.DeepEqual(verified, current) {
		return ManagedEnvironment{}, errors.Join(errors.New("environment source changed before registry publication"), err)
	}
	generationPath := filepath.Join(nameRoot, marker.Generation)
	if _, err := os.Lstat(generationPath); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(generationPath)
		existing, readErr := readManagedEnvironmentMarker(generationPath)
		if resolveErr != nil || resolved != generationPath || readErr != nil || !managedEnvironmentMarkerEquivalent(existing, marker) {
			return ManagedEnvironment{}, errors.New("environment adoption generation collides with current registry data")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := publishNamespaceRegistrationGeneration(nameRoot, marker, writeManagedEnvironmentMarker); err != nil {
			return ManagedEnvironment{}, err
		}
	} else {
		return ManagedEnvironment{}, err
	}
	if err := activateManagedEnvironment(root, marker.Name, generationPath); err != nil {
		return ManagedEnvironment{}, err
	}
	m.notifyRuntimeChange()
	return m.readManagedEnvironment(marker.Name, true)
}
