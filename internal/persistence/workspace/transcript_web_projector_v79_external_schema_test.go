package workspace

import (
	"reflect"
	"testing"
	"time"
)

func TestTranscriptWebProjectorV79PreservesUnrelatedExternalSchema(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if _, err := db.Exec(`CREATE VIEW unrelated_web_label AS SELECT 'transcript_web_messages' AS label;
		CREATE TRIGGER unrelated_project_note AFTER UPDATE OF name ON projects
		BEGIN SELECT 'transcript_web_projection_state'; /* transcript_web_message_identities */ END`); err != nil {
		t.Fatal(err)
	}
	definitions := func() string {
		t.Helper()
		var result string
		if err := db.QueryRow(`SELECT json_group_array(json_array(type,name,tbl_name,sql)) FROM
			(SELECT type,name,tbl_name,sql FROM sqlite_schema
			 WHERE name IN ('unrelated_web_label','unrelated_project_note') ORDER BY name)`).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := transcriptWebMigrationRows(t, db)
	schema := definitions()
	journal := readMigrationJournal(t, path)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err != nil {
		t.Fatalf("unrelated objects or text literals blocked the migration: %v", err)
	}
	assertTranscriptWebV79Integrity(t, db, 79)
	afterJournal := readMigrationJournal(t, path)
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) || schema != definitions() ||
		len(afterJournal) != 79 || !reflect.DeepEqual(journal, afterJournal[:78]) {
		t.Fatal("successful migration changed unrelated schema, retained rows or prior journal identities")
	}
	var label string
	if err := db.QueryRow(`SELECT label FROM unrelated_web_label`).Scan(&label); err != nil || label != "transcript_web_messages" {
		t.Fatalf("unrelated view no longer works: label=%q err=%v", label, err)
	}
	if _, err := db.Exec(`UPDATE projects SET name=name WHERE id='project-v79'`); err != nil {
		t.Fatalf("unrelated trigger no longer works: %v", err)
	}
}

func TestTranscriptWebProjectorV79RejectsShadowNamedInboundTrigger(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if _, err := db.Exec(`CREATE TRIGGER transcript_web_messages AFTER UPDATE OF name ON projects
		BEGIN SELECT message_json FROM transcript_web_messages WHERE stream_uid='stream-v79'; END`); err != nil {
		t.Fatalf("invalid controlled trigger in its separate SQLite namespace: %v", err)
	}
	before := transcriptWebMigrationRows(t, db)
	schema := readMigrationSchema(t, path)
	journal := readMigrationJournal(t, path)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err == nil {
		t.Fatal("canonical table name incorrectly authorized a separate inbound trigger")
	}
	assertTranscriptWebV79Integrity(t, db, 78)
	if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
		!reflect.DeepEqual(schema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
		t.Fatal("shadow-name rejection did not preserve rows, schema and migration journal")
	}
}
