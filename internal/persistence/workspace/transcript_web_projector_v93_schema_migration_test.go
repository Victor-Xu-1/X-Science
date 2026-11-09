package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebProjectorV93PreservesV14RowsAndMigrationHistory(t *testing.T) {
	db, path := newTranscriptWebV92RecoveryDatabase(t)
	before, journal := transcriptWebMigrationRows(t, db), readMigrationJournal(t, path)
	if len(journal) != 92 {
		t.Fatalf("predecessor migration count=%d want92", len(journal))
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 93); err != nil {
		t.Fatal(err)
	}
	assertTranscriptWebV93Integrity(t, db, 93)
	assertTranscriptWebProjectorVersionConstraint(t, db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11,12,13,14,15))")
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
		t.Fatal("migration relabeled old14 rows or changed source/retained data")
	}
	afterJournal := readMigrationJournal(t, path)
	checksum, err := transcriptWebProjectorV93Migration.validatedChecksum()
	if err != nil || len(afterJournal) != 93 || !reflect.DeepEqual(journal, afterJournal[:92]) ||
		afterJournal[92] != "93:transcript-web-projector-v15:"+checksum {
		t.Fatalf("migration changed prior identities or failed to append93: checksum=%s err=%v", checksum, err)
	}
	work, err := transcriptstore.NewWebReadModelRepository(db, db).ListTranscriptWebProjectionWork(t.Context(), "owner-v79", 10)
	if err != nil || len(work) != 1 || work[0].Reason != "projection_projector_version_stale" {
		t.Fatalf("old14 cache not left for the existing canonical rebuild: work=%+v err=%v", work, err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_projection_state SET projector_version=15`); err != nil {
		t.Fatalf("current15 rejected: %v", err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_web_projection_state SET projector_version=16`); err == nil {
		t.Fatal("unsupported16 accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertTranscriptWebV93Integrity(t, store.db, workspaceSchemaVersion)
	reopenedJournal := readMigrationJournal(t, path)
	if len(reopenedJournal) != workspaceSchemaVersion || len(reopenedJournal) < len(afterJournal) ||
		!reflect.DeepEqual(afterJournal, reopenedJournal[:len(afterJournal)]) {
		t.Fatal("normal reopen changed the immutable v93 journal prefix")
	}
	// Normal Open applies the current registry, including the separately tested
	// additive batch checkpoint migration. Its new table must remain empty;
	// all predecessor tables still require exact row equality.
	retained := transcriptWebMigrationRows(t, store.db)
	if rows, found := retained["literature_study_batches"]; !found || len(rows) != 0 {
		t.Fatalf("reopen batch checkpoint missing or populated: rows=%#v found=%t", rows, found)
	}
	delete(retained, "literature_study_batches")
	if !reflect.DeepEqual(before, retained) {
		t.Fatal("normal reopen reran migration or changed original rows")
	}
}

func TestTranscriptWebProjectorV93JournalFailureRollsBackAndRetries(t *testing.T) {
	db, path := newTranscriptWebV92RecoveryDatabase(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_projector93_journal BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=93 BEGIN SELECT RAISE(ABORT,'controlled projector93 journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	before, schema, journal := transcriptWebMigrationRows(t, db), readMigrationSchema(t, path), readMigrationJournal(t, path)
	err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 93)
	if err == nil || !strings.Contains(err.Error(), "controlled projector93 journal failure") {
		t.Fatalf("fixture did not reach journal failure: %v", err)
	}
	assertTranscriptWebV93Integrity(t, db, 92)
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
		!reflect.DeepEqual(schema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
		t.Fatal("journal failure left changed rows, schema or migration identities")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_projector93_journal`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 93); err != nil {
		t.Fatal(err)
	}
	assertTranscriptWebV93Integrity(t, db, 93)
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
		t.Fatal("successful retry changed retained rows")
	}
}

func TestTranscriptWebProjectorV93PreservesOrRejectsExternalSchema(t *testing.T) {
	for _, fixture := range []struct {
		name, statement string
		accept          bool
	}{
		{"unrelated_view", `CREATE VIEW recovery_external_view AS SELECT 1 AS retained`, true},
		{"inbound_view", `CREATE VIEW recovery_external_view AS SELECT message_id FROM transcript_web_messages`, false},
		{"unknown_child_column", `ALTER TABLE transcript_web_messages ADD COLUMN external_note TEXT;
			UPDATE transcript_web_messages SET external_note='retained extension bytes'`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			db, path := newTranscriptWebV92RecoveryDatabase(t)
			if _, err := db.Exec(fixture.statement); err != nil {
				t.Fatal(err)
			}
			before, schema, journal := transcriptWebMigrationRows(t, db), readMigrationSchema(t, path), readMigrationJournal(t, path)
			err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 93)
			if (err == nil) != fixture.accept {
				t.Fatalf("external schema accept=%t err=%v", fixture.accept, err)
			}
			if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
				t.Fatal("migration discarded retained source or derived bytes")
			}
			if !fixture.accept {
				assertTranscriptWebV93Integrity(t, db, 92)
				if !reflect.DeepEqual(schema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
					t.Fatal("rejected external dependency changed schema or journal")
				}
			} else {
				assertTranscriptWebV93Integrity(t, db, 93)
				var retained int
				if err := db.QueryRow(`SELECT retained FROM recovery_external_view`).Scan(&retained); err != nil || retained != 1 {
					t.Fatalf("unrelated external view changed: retained=%d err=%v", retained, err)
				}
			}
		})
	}
}

func newTranscriptWebV92RecoveryDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, path := newTranscriptWebV78Database(t)
	t.Cleanup(func() { _ = db.Close() })
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err != nil {
		t.Fatal(err)
	}
	// The historical target3 fixture includes a six-byte inline artifact with
	// size_bytes=0. Normalize through the same existing startup owner before
	// taking our baseline; later Open must not be mistaken for migration data loss.
	store := &Store{db: db}
	if err := store.ensureArtifactVersionStorageColumns(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	// Normal Open also installs these original startup-owned empty tables.
	// Include them in the baseline so all tables remain strictly comparable.
	if _, err := db.ExecContext(t.Context(), legacyMemoryCategoryAssignmentsDDL); err != nil {
		t.Fatal(err)
	}
	if err := store.ensureMutationResultLedgerSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE transcript_web_projection_state SET projector_version=14`); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func assertTranscriptWebV93Integrity(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	assertTranscriptWebV79Integrity(t, db, version)
	var staging int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN (
		'transcript_web_projection_state_v92','transcript_web_messages_v92',
		'transcript_web_message_identities_v92','transcript_web_message_artifact_refs_v92')`).Scan(&staging); err != nil || staging != 0 {
		t.Fatalf("migration left staging tables: count=%d err=%v", staging, err)
	}
}
