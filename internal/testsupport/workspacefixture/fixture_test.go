package workspacefixture

import (
	"context"
	"fmt"
	"sync"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestDatabasePathRetainsIsolationAfterTemplateOwnerCleanup(t *testing.T) {
	var cache templateCache
	t.Run("first_owner", func(t *testing.T) {
		path := cache.databasePath(t)
		store := openFixture(t, path)
		createProject(t, store, "first")
	})
	// The initializing child's directories have been cleaned up. A subsequent
	// caller must retain the schema without inheriting that child's project.
	path := cache.databasePath(t)
	store := openFixture(t, path)
	if _, found, err := store.GetProject("isolation-project"); err != nil || found {
		t.Fatalf("copied caller data: found=%t err=%v", found, err)
	}
	createProject(t, store, "second")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openFixture(t, path)
	project, found, err := reopened.GetProject("isolation-project")
	if err != nil || !found || project.Name != "second" {
		t.Fatalf("reopened project=%#v found=%t err=%v", project, found, err)
	}
}

func TestDatabasePathParallelCallersHaveIndependentStores(t *testing.T) {
	var cache templateCache
	var paths sync.Map
	for i := range 4 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			path := cache.databasePath(t)
			if _, duplicate := paths.LoadOrStore(path, true); duplicate {
				t.Fatalf("reused database path %s", path)
			}
			store := openFixture(t, path)
			createProject(t, store, t.Name())
			project, found, err := store.GetProject("isolation-project")
			if err != nil || !found || project.Name != t.Name() {
				t.Fatalf("project=%#v found=%t err=%v", project, found, err)
			}
		})
	}
}

func openFixture(t *testing.T, path string) *workspace.Store {
	t.Helper()
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	actual, err := store.SchemaStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := workspace.ExpectedSchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if actual.CurrentVersion != expected.CurrentVersion || actual.TargetVersion != expected.TargetVersion || len(actual.Migrations) != len(expected.Migrations) {
		t.Fatalf("schema version/count mismatch: actual=%#v expected=%#v", actual, expected)
	}
	for i, migration := range actual.Migrations {
		want := expected.Migrations[i]
		if migration.Version != want.Version || migration.Name != want.Name || migration.Checksum != want.Checksum || migration.RemediationID != want.RemediationID {
			t.Fatalf("schema migration %d identity changed", i)
		}
	}
	return store
}

func createProject(t *testing.T, store *workspace.Store, name string) {
	t.Helper()
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "isolation-project", UserID: "fixture-user", Name: name, Path: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
}
