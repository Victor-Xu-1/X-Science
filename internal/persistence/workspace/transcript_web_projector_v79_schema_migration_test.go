package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebProjectorV79PreservesV13RowsAndReopens(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	before := transcriptWebMigrationRows(t, db)
	journal := readMigrationJournal(t, path)
	if len(journal) != 78 {
		t.Fatalf("predecessor journal count=%d want78", len(journal))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertTranscriptWebV79Integrity(t, store.db, 79)
	assertTranscriptWebProjectorVersionConstraint(t, store.db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11,12,13,14))")
	if after := transcriptWebMigrationRows(t, store.db); !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed immutable source, derived rows, or old projector13 values")
	}
	afterJournal := readMigrationJournal(t, path)
	checksum, err := transcriptWebProjectorV79Migration.validatedChecksum()
	if err != nil || len(afterJournal) != 79 || !reflect.DeepEqual(journal, afterJournal[:78]) ||
		afterJournal[78] != "79:transcript-web-projector-v14:"+checksum {
		t.Fatalf("migration did not preserve the first78 identities and append its actual identity: journal=%+v checksum=%s err=%v", afterJournal, checksum, err)
	}
	work, err := transcriptstore.NewWebReadModelRepository(store.db, store.db).ListTranscriptWebProjectionWork(t.Context(), "owner-v79", 10)
	if err != nil || len(work) != 1 || work[0].Reason != "projection_projector_version_stale" {
		t.Fatalf("old13 was not left for the existing rebuild path: work=%+v err=%v", work, err)
	}
	tx, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_projection_state SET projector_version=14`); err != nil {
		t.Fatalf("current14 is rejected after migration: %v", err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_projection_state SET projector_version=15`); err == nil {
		t.Fatal("unsupported15 was accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrations(t.Context(), store.db, time.Now); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if after := transcriptWebMigrationRows(t, store.db); !reflect.DeepEqual(before, after) {
		t.Fatal("repeat changed the retained source or derived rows")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertTranscriptWebV79Integrity(t, reopened.db, 79)
	if !reflect.DeepEqual(afterJournal, readMigrationJournal(t, path)) ||
		!reflect.DeepEqual(before, transcriptWebMigrationRows(t, reopened.db)) {
		t.Fatal("reopen changed the journal or retained rows")
	}
}

func TestTranscriptWebProjectorV79JournalFailureRollsBackAndRetries(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	before := transcriptWebMigrationRows(t, db)
	journal := readMigrationJournal(t, path)
	const rejection = "controlled projector79 journal failure"
	if _, err := db.Exec(`CREATE TRIGGER reject_projector79_journal BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=79 BEGIN SELECT RAISE(ABORT,'controlled projector79 journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	beforeSchema := readMigrationSchema(t, path)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err == nil || !strings.Contains(err.Error(), rejection) {
		t.Fatalf("migration did not reach the controlled journal failure: %v", err)
	}
	assertTranscriptWebV79Integrity(t, db, 78)
	assertTranscriptWebProjectorVersionConstraint(t, db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11,12,13))")
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
		!reflect.DeepEqual(beforeSchema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
		t.Fatal("journal failure left changed DDL, application rows or migration identities")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_projector79_journal`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err != nil {
		t.Fatalf("retry after rolled-back failure: %v", err)
	}
	assertTranscriptWebV79Integrity(t, db, 79)
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
		t.Fatal("retry changed retained source, derived rows or old13 values")
	}
}

func assertTranscriptWebV79Integrity(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	for query, want := range map[string]int{
		`SELECT MAX(version) FROM workspace_schema_migrations`: version,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`:        0,
		`PRAGMA foreign_keys`:                                  1,
		`PRAGMA legacy_alter_table`:                            0,
		`SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('transcript_web_projection_state_v78','transcript_web_messages_v78',
			'transcript_web_message_identities_v78','transcript_web_message_artifact_refs_v78')`: 0,
	} {
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("integrity check %q got=%d want=%d err=%v", query, got, want, err)
		}
	}
}
