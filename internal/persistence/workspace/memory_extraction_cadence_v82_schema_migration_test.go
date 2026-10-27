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

func TestMemoryExtractionCadenceV82IsAdditiveAndPreservesV81Prefix(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 81); err != nil {
		t.Fatal(err)
	}
	before := transcriptWebMigrationRows(t, db)
	journal := readMigrationJournal(t, path)
	if len(journal) != 81 {
		t.Fatalf("source journal length=%d", len(journal))
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_memory_cadence BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=82 BEGIN SELECT RAISE(ABORT,'synthetic cadence migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 82); err == nil || !strings.Contains(err.Error(), "synthetic cadence migration failure") {
		t.Fatalf("missing controlled rollback: %v", err)
	}
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('project_runtime_metadata') WHERE name='memory_completion_ordinal'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("rollback column count=%d err=%v", columns, err)
	}
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
		t.Fatal("failed migration changed retained rows or history")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_memory_cadence`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 82); err != nil {
		t.Fatal(err)
	}
	checksum, err := memoryExtractionCadenceV82Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	latest := readMigrationJournal(t, path)
	if len(latest) != 82 || !reflect.DeepEqual(journal, latest[:81]) || latest[81] != "82:durable-memory-completion-cadence:"+checksum {
		t.Fatal("migration changed immutable v81 prefix")
	}
	if !reflect.DeepEqual(before, memoryExtractionRowsBeforeCadenceColumn(t, db)) {
		t.Fatal("additive cadence migration changed existing data")
	}
	var nonzero int
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_runtime_metadata WHERE memory_completion_ordinal<>0`).Scan(&nonzero); err != nil || nonzero != 0 {
		t.Fatalf("unexpected initial cadence=%d err=%v", nonzero, err)
	}
	if _, err := db.Exec(`INSERT INTO project_runtime_metadata(project_id) SELECT id FROM projects WHERE 1 ON CONFLICT(project_id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project_runtime_metadata SET memory_completion_ordinal=-1`); err == nil {
		t.Fatal("negative cadence ordinal accepted")
	}
	if _, err := db.Exec(`UPDATE project_runtime_metadata SET memory_completion_ordinal=1.5`); err == nil {
		t.Fatal("fractional cadence ordinal accepted")
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 82); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(latest, readMigrationJournal(t, path)) {
		t.Fatal("idempotent reopen changed migration identity")
	}
}

func memoryExtractionRowsBeforeCadenceColumn(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	result := transcriptWebMigrationRows(t, db)
	rows, err := db.Query(`SELECT project_id,description,context_data FROM project_runtime_metadata`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result["project_runtime_metadata"] = nil
	for rows.Next() {
		values, pointers := make([]any, 3), make([]any, 3)
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result["project_runtime_metadata"] = append(result["project_runtime_metadata"], fmt.Sprintf("%#v", values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(result["project_runtime_metadata"])
	return result
}
