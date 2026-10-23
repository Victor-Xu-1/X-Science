package workspace

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMemoryExtractionV81IdentityIsFrozen(t *testing.T) {
	const want = "1377681f2842d72d9ed38a957fd052b8756793940357b3436bff8e14ccba71c5"
	got, err := memoryExtractionV81Migration.validatedChecksum()
	if err != nil || got != want {
		t.Fatalf("schema81 canonical identity changed: checksum=%s err=%v want=%s", got, err, want)
	}
}

func TestMemoryExtractionV81MigrationRollsBackAndPreservesHistory(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 80); err != nil {
		t.Fatal(err)
	}
	journal := readMigrationJournal(t, path)
	if len(journal) != 80 {
		t.Fatalf("predecessor journal length=%d", len(journal))
	}
	// The historical fixture applies numbered migrations directly rather than
	// entering normal Open. Establish the unchanged v80 bootstrap first, proving
	// its three known compatibility effects before taking the migration baseline.
	expectedBootstrap := expectedMemoryExtractionBootstrapRows(t, db)
	bootstrap := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	if err := bootstrap.prepareLegacyWorkspaceSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.ensureMutationResultLedgerSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if actual := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(expectedBootstrap, actual) {
		assertMemoryExtractionMigrationRows(t, expectedBootstrap, actual)
		t.Fatal("predecessor bootstrap had an unaccounted effect")
	}
	if !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
		t.Fatal("predecessor bootstrap changed numbered migration identities")
	}
	t.Log("predecessor bootstrap: only legacy report size 0->6 and two empty compatibility tables; bytes, IDs, timestamps and all other rows unchanged")
	before := transcriptWebMigrationRows(t, db)
	if _, err := db.Exec(`CREATE TRIGGER fail_extraction_migration BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=81 BEGIN SELECT RAISE(ABORT,'injected extraction migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 81); err == nil || !strings.Contains(err.Error(), "injected extraction migration failure") {
		t.Fatalf("migration did not reach controlled journal failure: %v", err)
	}
	var targets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN
		('memory_extraction_receipts','memory_extraction_receipts_frame_idx','memory_extraction_receipts_immutable')`).Scan(&targets); err != nil || targets != 0 {
		t.Fatalf("migration rollback left target objects=%d err=%v", targets, err)
	}
	if !reflect.DeepEqual(journal, readMigrationJournal(t, path)) || !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
		t.Fatal("failed additive migration changed predecessor journal or retained source")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_extraction_migration`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 81); err != nil {
		t.Fatal(err)
	}
	checksum, err := memoryExtractionV81Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	current := readMigrationJournal(t, path)
	if len(current) != 81 || !reflect.DeepEqual(journal, current[:80]) || current[80] != "81:atomic-memory-extraction-receipts:"+checksum {
		t.Fatal("migration did not append only its own immutable identity")
	}
	retained := transcriptWebMigrationRows(t, db)
	if rows, found := retained["memory_extraction_receipts"]; !found || len(rows) != 0 {
		t.Fatal("new receipt table was missing or not empty")
	}
	delete(retained, "memory_extraction_receipts")
	if !reflect.DeepEqual(before, retained) {
		t.Fatal("additive receipt migration changed retained transcript/artifact rows")
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 81); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, err := reopened.SchemaStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentVersion != workspaceSchemaVersion || status.TargetVersion != workspaceSchemaVersion {
		t.Fatalf("reopen schema current=%d target=%d want%d", status.CurrentVersion, status.TargetVersion, workspaceSchemaVersion)
	}
	reopenedJournal := readMigrationJournal(t, path)
	if len(reopenedJournal) < len(current) || !reflect.DeepEqual(current, reopenedJournal[:len(current)]) {
		t.Fatal("reopen changed the existing v81 schema journal prefix")
	}
	// The current binary appends only the dedicated v82 cadence column. Compare
	// the exact v81 projection; the v82 regression checks its new value/constraint.
	retained = memoryExtractionRowsBeforeCadenceColumn(t, reopened.db)
	delete(retained, "memory_extraction_receipts")
	assertMemoryExtractionMigrationRows(t, before, retained)
	var violations int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign key violations=%d err=%v", violations, err)
	}
}

func assertMemoryExtractionMigrationRows(t *testing.T, before, retained map[string][]string) {
	t.Helper()
	for name, oldRows := range before {
		newRows, found := retained[name]
		if !found || !reflect.DeepEqual(oldRows, newRows) {
			t.Errorf("reopen changed retained table %q: before=%#v after=%#v found=%t", name, oldRows, newRows, found)
		}
	}
	for name, newRows := range retained {
		if _, found := before[name]; !found {
			t.Errorf("reopen added table %q outside migration81: rows=%#v", name, newRows)
		}
	}
	return
}

func expectedMemoryExtractionBootstrapRows(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	expected := transcriptWebMigrationRows(t, db)
	for _, table := range []string{"memory_category_assignments", "mutation_result_ledger"} {
		if _, found := expected[table]; found {
			t.Fatalf("historical predecessor already contains bootstrap-only table %q", table)
		}
		expected[table] = nil
	}
	rows, err := db.Query(`SELECT * FROM artifact_versions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	indices := make(map[string]int, len(columns))
	for index, column := range columns {
		indices[column] = index
	}
	var artifacts []string
	corrected := 0
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		if values[indices["id"]] == "version" {
			if values[indices["content"]] != "report" || values[indices["size_bytes"]] != int64(0) {
				t.Fatal("historical report fixture changed before size-only bootstrap")
			}
			values[indices["size_bytes"]] = int64(6)
			corrected++
		}
		artifacts = append(artifacts, fmt.Sprintf("%#v", values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if corrected != 1 {
		t.Fatalf("historical size-only correction count=%d want1", corrected)
	}
	sort.Strings(artifacts)
	expected["artifact_versions"] = artifacts
	return expected
}
