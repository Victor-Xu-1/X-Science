package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

func TestManagedEndpointOwnerV78UpgradePreservesDataAndJournal(t *testing.T) {
	t.Run("normal_upgrade_and_reopen", func(t *testing.T) {
		fixture := newManagedEndpointV77UpgradeFixture(t)
		if err := fixture.db.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := Open(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		after, err := store.SchemaStatus(t.Context())
		if err != nil || after.CurrentVersion != 78 || after.TargetVersion != 78 || len(after.Migrations) != 78 {
			t.Fatalf("normal Open did not migrate installed77 to owner78: %+v %v", after, err)
		}
		fixture.assertRetained(t, store.db)
		if store.schemaBackupPath == "" {
			t.Fatal("schema77 upgrade backup missing")
		}
		backup, err := sql.Open(sqliteDriver, store.schemaBackupPath)
		if err != nil {
			t.Fatal(err)
		}
		defer backup.Close()
		assertSchemaJournalVersion(t, backup, 77)
		fixture.assertRetained(t, backup)
		assertManagedEndpointV78ObjectsAbsent(t, backup)
		var invented int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM compute_managed_endpoints WHERE owner_spec_json IS NOT NULL`).Scan(&invented); err != nil || invented != 0 {
			t.Fatalf("migration invented legacy owner metadata: %d %v", invented, err)
		}
		for _, table := range []string{"compute_managed_endpoint_instances", "compute_managed_endpoint_actions"} {
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&invented); err != nil || invented != 0 {
				t.Fatalf("migration invented %s evidence: %d %v", table, invented, err)
			}
		}
		for _, relationship := range []struct{ table, parent string }{
			{"compute_managed_endpoint_instances", "compute_managed_endpoints"},
			{"compute_managed_endpoint_actions", "compute_managed_endpoint_instances"},
		} {
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE "table"=? AND on_delete='RESTRICT'`, relationship.table, relationship.parent).Scan(&count); err != nil || count != 1 {
				t.Fatalf("owner retention FK %s->%s: %d %v", relationship.table, relationship.parent, count, err)
			}
		}
		checksum, err := workspaceSchemaMigrations[77].validatedChecksum()
		if err != nil || after.Migrations[77].Version != 78 || after.Migrations[77].Checksum != checksum {
			t.Fatalf("owner journal identity is not the registered78: %+v %v", after.Migrations[77], err)
		}
		if err := applyVersionedSchemaMigrations(t.Context(), store.db, time.Now); err != nil {
			t.Fatal(err)
		}
		if repeated, err := store.SchemaStatus(t.Context()); err != nil || !reflect.DeepEqual(after, repeated) {
			t.Fatalf("repeat migration changed journal: %+v %v", repeated, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if repeated, err := reopened.SchemaStatus(t.Context()); err != nil || !reflect.DeepEqual(after, repeated) || reopened.schemaBackupPath != "" {
			t.Fatalf("reopen changed journal or created another backup: %+v %v", repeated, err)
		}
		fixture.assertRetained(t, reopened.db)
		assertManagedEndpointV78TypedIntent(t, reopened)
	})

	t.Run("partial_target_is_not_adopted", func(t *testing.T) {
		fixture := newManagedEndpointV77UpgradeFixture(t)
		if _, err := fixture.db.Exec(`ALTER TABLE compute_managed_endpoints ADD COLUMN owner_spec_json TEXT`); err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := Open(fixture.path)
		if err == nil {
			_ = store.Close()
			t.Fatal("normal Open silently accepted an unjournaled partial owner migration target")
		}
		retained, err := sql.Open(sqliteDriver, fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		assertSchemaJournalVersion(t, retained, 77)
		fixture.assertRetained(t, retained)
		var count int
		if err := retained.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN
			('compute_managed_endpoint_instances','compute_managed_endpoint_actions',
			'compute_managed_endpoint_active_owner_idx','compute_managed_endpoint_pending_action_idx')`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed preflight left owner evidence objects: %d %v", count, err)
		}
	})

	t.Run("journal_failure_rolls_back_all_owner_ddl", func(t *testing.T) {
		fixture := newManagedEndpointV77UpgradeFixture(t)
		if _, err := fixture.db.Exec(`CREATE TRIGGER fail_owner_v78_journal BEFORE INSERT ON workspace_schema_migrations
			WHEN NEW.version=78 BEGIN SELECT RAISE(ABORT,'injected owner migration journal failure'); END`); err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := Open(fixture.path)
		if err == nil {
			_ = store.Close()
			t.Fatal("normal Open did not reach the injected78 journal failure")
		}
		if !strings.Contains(err.Error(), "injected owner migration journal failure") {
			t.Fatalf("migration failed before the intended journal boundary: %v", err)
		}
		retained, err := sql.Open(sqliteDriver, fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		assertSchemaJournalVersion(t, retained, 77)
		fixture.assertRetained(t, retained)
		assertManagedEndpointV78ObjectsAbsent(t, retained)
		if _, err := retained.Exec(`DROP TRIGGER fail_owner_v78_journal`); err != nil {
			t.Fatal(err)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		retried, err := Open(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		defer retried.Close()
		assertSchemaJournalVersion(t, retried.db, 78)
		fixture.assertRetained(t, retried.db)
	})
}

func assertManagedEndpointV78ObjectsAbsent(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('compute_managed_endpoints') WHERE name='owner_spec_json'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("owner column unexpectedly present: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN
		('compute_managed_endpoint_instances','compute_managed_endpoint_actions',
		'compute_managed_endpoint_active_owner_idx','compute_managed_endpoint_pending_action_idx')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("owner evidence objects unexpectedly present: %d %v", count, err)
	}
}

func assertManagedEndpointV78TypedIntent(t *testing.T, store *Store) {
	t.Helper()
	registration := compute.ManagedEndpointRegistration{Name: "post-upgrade-owned", URL: "http://127.0.0.1:25101", Port: 25101,
		SkillName: "using-model-endpoint", StartScript: "printf start", StopScript: "printf stop", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	if err := store.UpsertManagedEndpoint(ManagedEndpoint{Name: registration.Name, URL: registration.URL, Port: registration.Port,
		SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath,
		OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), State: "stopped", Location: "local",
		RegisteredBy: "migration-owner", ServiceDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	claimed, run, err := store.ClaimManagedEndpointStart(registration.Name, "migration-owner", root)
	if err != nil || !run {
		t.Fatalf("normal migrated Store cannot claim typed intent: run=%t %v", run, err)
	}
	instance, action, err := store.PrepareManagedEndpointAction(t.Context(), managedEndpointOwnerTestClaim(claimed, registration), "unused-existing-intent", root)
	if err != nil || instance.ID == "" || action.Accepted || action.Receipt != nil {
		t.Fatalf("normal migrated Store lost unaccepted intent: %+v %+v %v", instance, action, err)
	}
	_, read, err := store.ReadManagedEndpointAction(t.Context(), action.Ref)
	if err != nil || read.Ref != action.Ref || read.Accepted || read.Receipt != nil {
		t.Fatalf("normal migrated Store intent readback: %+v %v", read, err)
	}
}
