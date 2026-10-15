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
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err != nil {
		t.Fatal(err)
	}
	assertTranscriptWebV79Integrity(t, db, 79)
	assertTranscriptWebProjectorVersionConstraint(t, db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11,12,13,14))")
	if after := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed immutable source, derived rows, or old projector13 values")
	}
	afterJournal := readMigrationJournal(t, path)
	checksum, err := transcriptWebProjectorV79Migration.validatedChecksum()
	if err != nil || len(afterJournal) != 79 || !reflect.DeepEqual(journal, afterJournal[:78]) ||
		afterJournal[78] != "79:transcript-web-projector-v14:"+checksum {
		t.Fatalf("migration did not preserve the first78 identities and append its actual identity: journal=%+v checksum=%s err=%v", afterJournal, checksum, err)
	}
	work, err := transcriptstore.NewWebReadModelRepository(db, db).ListTranscriptWebProjectionWork(t.Context(), "owner-v79", 10)
	if err != nil || len(work) != 1 || work[0].Reason != "projection_projector_version_stale" {
		t.Fatalf("old13 was not left for the existing rebuild path: work=%+v err=%v", work, err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
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
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if after := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("repeat changed the retained source or derived rows")
	}
	// The exact79 boundary above owns its complete row/DDL assertions. Normal
	// Open may legitimately add later tables/columns, so retain this fixture's
	// source/cache values without fixing the future current schema to79.
	retainedValues := func(db *sql.DB) string {
		t.Helper()
		var values string
		if err := db.QueryRow(`SELECT json_array(
			(SELECT hex(payload_json) FROM transcript_events WHERE stream_uid='stream-v79' AND client_message_id='client-v79'),
			(SELECT json_array(message_id,client_message_id,message_json,message_sha256,visible_index)
				FROM transcript_web_messages WHERE stream_uid='stream-v79' AND ordinal=1),
			(SELECT json_group_array(json_array(identity,kind)) FROM
				(SELECT identity,kind FROM transcript_web_message_identities WHERE stream_uid='stream-v79' ORDER BY identity)),
			(SELECT json_array(source_event_id,artifact_id,version_id,relation,filename,checksum,size_bytes)
				FROM transcript_web_message_artifact_refs WHERE stream_uid='stream-v79' AND message_ordinal=1 AND ordinal=0),
			(SELECT content_sha256 FROM artifact_versions WHERE artifact_id='artifact-v79'))`).Scan(&values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	retained := retainedValues(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertTranscriptWebV79Integrity(t, store.db, workspaceSchemaVersion)
	status, err := store.SchemaStatus(t.Context())
	currentJournal := readMigrationJournal(t, path)
	if err != nil || status.CurrentVersion != workspaceSchemaVersion || status.TargetVersion != workspaceSchemaVersion ||
		len(currentJournal) != workspaceSchemaVersion || len(currentJournal) < 79 || !reflect.DeepEqual(afterJournal, currentJournal[:79]) ||
		retainedValues(store.db) != retained {
		t.Fatalf("normal Open did not preserve79 identity/data while reaching the current target: status=%+v journal=%+v err=%v", status, currentJournal, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertTranscriptWebV79Integrity(t, reopened.db, workspaceSchemaVersion)
	if status, err := reopened.SchemaStatus(t.Context()); err != nil || status.CurrentVersion != workspaceSchemaVersion || status.TargetVersion != workspaceSchemaVersion ||
		!reflect.DeepEqual(currentJournal, readMigrationJournal(t, path)) || retainedValues(reopened.db) != retained {
		t.Fatalf("reopen changed the current target, journal or retained values: status=%+v err=%v", status, err)
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

func TestTranscriptWebProjectorV79RejectsNoncanonicalReplacementSources(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{"child_column_with_data", `ALTER TABLE transcript_web_messages ADD COLUMN external_note TEXT;
			UPDATE transcript_web_messages SET external_note='retained external column bytes'`},
		{"extra_child_index", `CREATE INDEX external_web_message_hash ON transcript_web_messages(message_sha256)`},
		{"extra_child_trigger", `CREATE TRIGGER external_web_message_update BEFORE UPDATE ON transcript_web_messages
			BEGIN SELECT 1; END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, path := newTranscriptWebV78Database(t)
			if _, err := db.Exec(test.statement); err != nil {
				t.Fatalf("invalid controlled source extension: %v", err)
			}
			if test.name == "child_column_with_data" {
				var retained string
				if err := db.QueryRow(`SELECT external_note FROM transcript_web_messages WHERE stream_uid='stream-v79'`).Scan(&retained); err != nil || retained != "retained external column bytes" {
					t.Fatalf("extended child has no actual value to preserve: value=%q err=%v", retained, err)
				}
			}
			before := transcriptWebMigrationRows(t, db)
			schema := readMigrationSchema(t, path)
			journal := readMigrationJournal(t, path)
			if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err == nil {
				t.Fatal("migration accepted a noncanonical source and could discard its data or logic")
			}
			assertTranscriptWebV79Integrity(t, db, 78)
			if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
				!reflect.DeepEqual(schema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
				t.Fatal("source rejection changed application data, schema or migration identities")
			}
		})
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
