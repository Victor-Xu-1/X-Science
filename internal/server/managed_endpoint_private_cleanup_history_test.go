//go:build linux

package server

import (
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/compute"
)

func TestManagedEndpointPrivateCleanupPreservesRetiredScopeAfterReregistration(t *testing.T) {
	fixture := newManagedEndpointPrivateCleanupFixture(t)
	endpoints, err := fixture.store.ListManagedEndpoints(fixture.instance.UserID, fixture.instance.Endpoint, false)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("read stopped registration: %+v %v", endpoints, err)
	}
	updated := endpoints[0]
	updated.StartScript = "printf updated"
	updated.ServiceDir = t.TempDir()
	updated.ApprovedScriptHash = compute.ApprovedManagedEndpointHash(managedEndpointRegistration(updated))
	if err := fixture.store.UpsertManagedEndpoint(updated); err != nil {
		t.Fatal(err)
	}
	newCache := filepath.Join(updated.ServiceDir, "cache", "new-model-weights")
	writeManagedEndpointPrivateCleanupFile(t, newCache, []byte("new registration cache"))
	if deleted, err := fixture.store.DeleteManagedEndpoint(updated.Name, updated.RegisteredBy); err != nil || !deleted {
		t.Fatalf("current registration was imposed on valid retired history: %v %v", deleted, err)
	}
	runManagedEndpointPrivateCleanupRound(t, fixture.app)
	if _, err := os.Lstat(fixture.directory); !os.IsNotExist(err) {
		t.Errorf("original retired scope was not reclaimed: %v", err)
	}
	for _, path := range []string{fixture.cache, newCache, fixture.unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("retired cleanup removed unrelated cache/owner %q: %v", path, err)
		}
	}
}
