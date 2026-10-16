package workspace

import (
	"reflect"
	"testing"
	"time"
)

func TestTranscriptWebProjectorV79PreservesExternalSchemaDependencies(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{"external_view", `CREATE VIEW external_web_message_view AS
			SELECT stream_uid,message_json FROM transcript_web_messages`},
		{"external_trigger", `CREATE TRIGGER external_frame_web_lookup AFTER UPDATE OF name ON projects
			BEGIN SELECT message_json FROM transcript_web_messages WHERE stream_uid='stream-v79'; END`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, path := newTranscriptWebV78Database(t)
			if _, err := db.Exec(test.statement); err != nil {
				t.Fatalf("invalid controlled inbound schema dependency: %v", err)
			}
			before := transcriptWebMigrationRows(t, db)
			schema := readMigrationSchema(t, path)
			journal := readMigrationJournal(t, path)
			if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 79); err == nil {
				t.Fatal("migration accepted an external schema dependency that SQLite can rewrite to a retired staging table")
			}
			assertTranscriptWebV79Integrity(t, db, 78)
			if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
				!reflect.DeepEqual(schema, readMigrationSchema(t, path)) || !reflect.DeepEqual(journal, readMigrationJournal(t, path)) {
				t.Fatal("dependency rejection changed source data, external schema, derived rows or migration journal")
			}
		})
	}
}
