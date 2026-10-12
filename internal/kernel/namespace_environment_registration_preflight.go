package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// PreflightManagedEnvironmentNamespaceImports checks every genuine frozen
// source and every target collision before the first registry publication.
// Publication is individually atomic/idempotent; later source drift is an
// explicit resumable error rather than a reason to rewrite either authority.
func (m *Manager) PreflightManagedEnvironmentNamespaceImports(ctx context.Context, imports []NamespaceEnvironmentImport) error {
	if m == nil || ctx == nil {
		return errors.New("environment import preflight requires a manager and context")
	}
	if err := validateNamespaceRegistryTargetRoot(m.config.CondaEnvsPath); err != nil {
		return err
	}
	root, err := m.managedEnvironmentRoot()
	if err != nil || root != filepath.Clean(m.config.CondaEnvsPath) {
		return errors.New("environment import registry root is unavailable or aliased")
	}
	if len(imports) > 4096 {
		return errors.New("environment import inventory is oversized")
	}
	seen := map[string]bool{}
	for _, frozen := range imports {
		root, _, marker, err := m.prepareNamespaceEnvironmentImport(ctx, frozen)
		if err != nil {
			return err
		}
		if seen[marker.Name] {
			return errors.New("environment imports collide on a current resource identity")
		}
		seen[marker.Name] = true
		if err := validateNamespaceRegistryGenerationRoot(root, marker.Name); err != nil {
			return err
		}
		active := filepath.Join(root, marker.Name)
		if _, err := os.Lstat(active); err == nil {
			prefix, resolveErr := filepath.EvalSymlinks(active)
			if resolveErr != nil || prefix != filepath.Join(root, ".generations", marker.Name, marker.Generation) {
				return errors.New("environment import collides with an existing active resource")
			}
			existing, readErr := readManagedEnvironmentMarker(prefix)
			if readErr != nil || !managedEnvironmentMarkerEquivalent(existing, marker) {
				return errors.New("environment import would overwrite an existing logical resource")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		generationRoot := filepath.Join(root, ".generations", marker.Name)
		if resolved, err := filepath.EvalSymlinks(generationRoot); err == nil && resolved != generationRoot {
			return errors.New("environment import target generation root is aliased")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		generation := filepath.Join(generationRoot, marker.Generation)
		if _, err := os.Lstat(generation); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(generation)
			existing, readErr := readManagedEnvironmentMarker(generation)
			if resolveErr != nil || resolved != generation || readErr != nil || !managedEnvironmentMarkerEquivalent(existing, marker) {
				return errors.New("environment import generation collides with current registry data")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func validateNamespaceRegistryGenerationRoot(root, name string) error {
	for _, path := range []string{filepath.Join(root, ".generations"), filepath.Join(root, ".generations", name)} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("environment import registry generation directory is unavailable or aliased")
		}
	}
	return nil
}

// Check the nearest existing ancestor before any MkdirAll. Evaluating that
// exact lexical ancestor also rejects hidden intermediate symlinks, so an
// absent target child cannot cause writes inside a foreign physical tree.
func validateNamespaceRegistryTargetRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Dir(root) == root {
		return errors.New("environment import target must be a bounded absolute directory")
	}
	for ancestor := root; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("environment import target ancestor is unavailable or aliased")
		}
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err != nil || resolved != ancestor {
			return errors.New("environment import target ancestor resolves to a foreign directory")
		}
		return nil
	}
}

func (m *Manager) prepareNamespaceEnvironmentImport(ctx context.Context, frozen NamespaceEnvironmentImport) (string, NamespaceEnvironmentImport, managedEnvironmentMarker, error) {
	if m == nil || ctx == nil || frozen.source.SourceSHA256 == "" || frozen.SourceSHA256 != frozen.source.SourceSHA256 {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, errors.New("environment adoption requires a genuine frozen source observation")
	}
	if err := validateNamespaceRegistryTargetRoot(m.config.CondaEnvsPath); err != nil {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, err
	}
	root, err := m.managedEnvironmentRoot()
	if err != nil || root != filepath.Clean(m.config.CondaEnvsPath) || root == frozen.source.SourceRoot || hostPathContains(frozen.source.SourceRoot, root) || hostPathContains(root, frozen.source.SourceRoot) {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, errors.New("environment adoption source and registry roots must be independent")
	}
	current, err := inspectNamespaceEnvironmentSource(ctx, frozen.source.SourceRoot, frozen.SourceName)
	if err != nil || !reflect.DeepEqual(current, frozen) {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, errors.Join(errors.New("environment adoption source changed after inspection"), err)
	}
	original, err := decodeNamespaceRegistrationSource(current.source)
	if err != nil {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, err
	}
	marker := original
	marker.PreviousNamespace = nil
	marker.Name, marker.Kind = current.CurrentName, namespaceRegisteredCondaKind
	marker.SourcePath, marker.RuntimePath = current.SourcePrefix, current.SourcePrefix
	marker.Operation, marker.OperationKey = "namespace-register", current.SourceSHA256
	marker.NamespaceImport = &current.source
	if _, err := validateNamespaceRegisteredEnvironmentMarker(marker); err != nil {
		return "", NamespaceEnvironmentImport{}, managedEnvironmentMarker{}, err
	}
	return root, current, marker, nil
}
