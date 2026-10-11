package workspace

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func projectIdentityV72Fixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	ctx := context.Background()
	if err := prepareSchemaJournal(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.prepareLegacyWorkspaceSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 72); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestProjectIdentityV73UpgradeSeedsActiveIDsWithoutChangingData(t *testing.T) {
	store := projectIdentityV72Fixture(t)
	ctx := context.Background()
	directory := t.TempDir()
	evidence := filepath.Join(directory, "evidence.txt")
	if err := os.WriteFile(evidence, []byte("retained evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(CreateProjectInput{ID: "seeded", UserID: "alice", Name: "Existing", Path: directory})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.SchemaStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.CurrentVersion != 72 {
		t.Fatalf("fixture schema=%d", before.CurrentVersion)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 73); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 73); err != nil {
		t.Fatal(err)
	}
	after, err := store.SchemaStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentVersion != 73 || len(after.Migrations) != 73 {
		t.Fatalf("upgrade journal=%#v", after)
	}
	for index, original := range before.Migrations {
		if after.Migrations[index] != original {
			t.Fatalf("old migration %d identity or timestamp changed", original.Version)
		}
	}
	retained, found, err := store.GetProject(project.ID)
	if err != nil || !found || retained != project {
		t.Fatalf("existing project changed: %#v found=%v err=%v", retained, found, err)
	}
	newName := "Mutable field remains writable"
	if _, err := store.UpdateProject(project.ID, UpdateProjectInput{Name: &newName}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: project.ID, UserID: "bob", Name: "Replacement"}); err == nil {
		t.Fatal("seeded project identity was reused after deletion")
	}
	if raw, err := os.ReadFile(evidence); err != nil || string(raw) != "retained evidence" {
		t.Fatalf("managed evidence was removed: %q err=%v", raw, err)
	}
	var history int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM project_id_history WHERE id=?", project.ID).Scan(&history); err != nil || history != 1 {
		t.Fatalf("history=%d err=%v", history, err)
	}
}

func TestProjectIdentityV73MigrationFailureIsAtomic(t *testing.T) {
	store := projectIdentityV72Fixture(t)
	ctx := context.Background()
	if _, err := store.CreateProject(CreateProjectInput{ID: "seeded", UserID: "alice", Name: "Existing"}); err != nil {
		t.Fatal(err)
	}
	failed := projectIdentityV73Migration
	failed.statements = append(append([]string(nil), failed.statements...), "INSERT INTO deliberately_absent_migration_table VALUES(1)")
	checksum, err := failed.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigration(ctx, store.db, time.Now, failed, checksum); err == nil {
		t.Fatal("controlled migration failure succeeded")
	}
	var objects, version int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('project_id_history','projects_identity_guard','projects_identity_register','projects_identity_immutable')").Scan(&objects); err != nil || objects != 0 {
		t.Fatalf("partial migration objects=%d err=%v", objects, err)
	}
	if err := store.db.QueryRow("SELECT MAX(version) FROM workspace_schema_migrations").Scan(&version); err != nil || version != 72 {
		t.Fatalf("failed migration journal=%d err=%v", version, err)
	}
	if _, found, err := store.GetProject("seeded"); err != nil || !found {
		t.Fatalf("migration lost project: found=%v err=%v", found, err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 73); err != nil {
		t.Fatalf("forward retry failed: %v", err)
	}
}

func TestProjectIdentityV73HasCanonicalIdentity(t *testing.T) {
	expected, err := ExpectedSchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	last := expected.Migrations[len(expected.Migrations)-1]
	if last.Version != 73 || last.Name != "project-identity-history" {
		t.Fatalf("last migration=%#v", last)
	}
	const published = "5dd28130de13a1bd1b8b931abe480167ca00586887f5ded0eeaf731743e3f37c"
	if last.Checksum != published {
		t.Fatalf("v73 identity changed: got %s want %s", last.Checksum, published)
	}
}
