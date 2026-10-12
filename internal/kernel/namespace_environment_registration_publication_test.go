package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func registrationPublicationFixture(t *testing.T) (*Manager, NamespaceEnvironmentImport, string, managedEnvironmentMarker) {
	t.Helper()
	source, _, _, _ := namespaceRegistrationFixture(t, "user-scientific-env")
	imports, err := InspectManagedEnvironmentNamespaceImports(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{CondaEnvsPath: t.TempDir()})
	root, _, marker, err := manager.prepareNamespaceEnvironmentImport(context.Background(), imports[0])
	if err != nil {
		t.Fatal(err)
	}
	nameRoot := filepath.Join(root, ".generations", marker.Name)
	if err := os.MkdirAll(nameRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return manager, imports[0], nameRoot, marker
}

func TestNamespaceEnvironmentRegistrationWriteFailureLeavesNoFinalAndRetrySucceeds(t *testing.T) {
	manager, frozen, nameRoot, marker := registrationPublicationFixture(t)
	writeFailure := func(path string, marker managedEnvironmentMarker) error {
		// Force a real filesystem error in the production atomic marker writer,
		// rather than faking a successful receipt or changing source identity.
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		return writeManagedEnvironmentMarker(path, marker)
	}
	if err := publishNamespaceRegistrationGeneration(nameRoot, marker, writeFailure); err == nil {
		t.Fatal("marker writer unexpectedly replaced a directory")
	}
	final := filepath.Join(nameRoot, marker.Generation)
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		t.Fatal("failed marker write left an empty final generation")
	}
	if entries, err := os.ReadDir(nameRoot); err != nil || len(entries) != 0 {
		t.Fatal("failed publication left this invocation's private staging")
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), []NamespaceEnvironmentImport{frozen}); err != nil {
		t.Fatal(err)
	}
	if adopted, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), frozen); err != nil || adopted.Generation != frozen.Generation {
		t.Fatalf("same-source retry failed: %+v %v", adopted, err)
	}
}

func TestNamespaceEnvironmentRegistrationInterruptedPrivateStagingDoesNotBlockRetry(t *testing.T) {
	manager, frozen, nameRoot, _ := registrationPublicationFixture(t)
	orphan, err := os.MkdirTemp(nameRoot, namespaceRegistrationStagingPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, ".interrupted-marker.tmp"), []byte("incomplete private write"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), []NamespaceEnvironmentImport{frozen}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), frozen); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("retry deleted another invocation's non-authoritative staging")
	}
}

func TestNamespaceEnvironmentRegistrationPublicationNeverReplacesForeignFinal(t *testing.T) {
	manager, frozen, nameRoot, marker := registrationPublicationFixture(t)
	final := filepath.Join(nameRoot, marker.Generation)
	if err := os.Mkdir(final, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(final, "owner-data")
	if err := os.WriteFile(foreign, []byte("keep original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishNamespaceRegistrationGeneration(nameRoot, marker, writeManagedEnvironmentMarker); err == nil {
		t.Fatal("unknown final generation was replaced")
	}
	if err := manager.PreflightManagedEnvironmentNamespaceImports(context.Background(), []NamespaceEnvironmentImport{frozen}); err == nil {
		t.Fatal("unknown final generation passed preflight")
	}
	if _, err := manager.AdoptManagedEnvironmentNamespaceImport(context.Background(), frozen); err == nil {
		t.Fatal("unknown final generation was adopted")
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "keep original" {
		t.Fatal("foreign final data was overwritten or removed")
	}
}

func TestNamespaceEnvironmentRegistrationExclusivePublishClosesDestinationRace(t *testing.T) {
	_, _, nameRoot, marker := registrationPublicationFixture(t)
	final := filepath.Join(nameRoot, marker.Generation)
	writer := func(path string, marker managedEnvironmentMarker) error {
		if err := writeManagedEnvironmentMarker(path, marker); err != nil {
			return err
		}
		// A new empty directory after the initial check is still foreign; plain
		// POSIX rename could overwrite it, but exclusive publication must not.
		return os.Mkdir(final, 0o700)
	}
	if err := publishNamespaceRegistrationGeneration(nameRoot, marker, writer); err == nil {
		t.Fatal("destination race replaced an unknown empty final directory")
	}
	if entries, err := os.ReadDir(final); err != nil || len(entries) != 0 {
		t.Fatal("destination race changed the foreign directory")
	}
}
