package workspace

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const libraryAnnotationOriginalRows = `SELECT printf('%s:%s:%s:%s:%d:%s:%s:%s:%s',hex(id),hex(project_id),hex(target_kind),hex(target_key),label_idx,hex(content_checksum),hex(body),hex(CAST(created_at AS BLOB)),hex(CAST(updated_at AS BLOB))) FROM annotations ORDER BY id`

func TestLibraryAnnotationV89PreservesOriginalRowsAndRollsBack(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 88); err != nil {
		t.Fatal(err)
	}
	var project string
	if err := db.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO annotations(id,project_id,target_kind,target_key,label_idx,content_checksum,body,created_at) VALUES('preserved-note',?,'artifact','av:original',0,'same-checksum','{"text":"保留 🧪"}','2026-01-01')`, project); err != nil {
		t.Fatal(err)
	}
	before := literatureIdentityV83Values(t, db, libraryAnnotationOriginalRows)
	if _, err := db.Exec(`CREATE TRIGGER reject_library_annotation_marker BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=89 BEGIN SELECT RAISE(ABORT,'injected annotation marker failure');END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 89); err == nil || !strings.Contains(err.Error(), "injected annotation marker failure") {
		t.Fatalf("expected transactional rollback: %v", err)
	}
	if got := literatureIdentityV83Values(t, db, libraryAnnotationOriginalRows); !reflect.DeepEqual(before, got) {
		t.Fatal("failed migration changed old annotation values")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_library_annotation_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 89); err != nil {
		t.Fatal(err)
	}
	if got := literatureIdentityV83Values(t, db, libraryAnnotationOriginalRows); !reflect.DeepEqual(before, got) {
		t.Fatal("migration changed old annotation values")
	}
	if _, err := db.Exec(`INSERT INTO annotations(id,target_kind,target_key,label_idx,body,created_at) VALUES('missing-owner','literature-attachment-version','litv:litv_missing',0,'{}','2026-01-01')`); err == nil {
		t.Fatal("NULL Library owner bypassed scope CHECK")
	}
	if _, err := db.Exec(`INSERT INTO annotations(id,library_owner_id,target_kind,target_key,label_idx,body,created_at) VALUES('independent','owner-independent','literature-attachment-version','litv:litv_retained',0,'{}','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id=?`, project); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM annotations WHERE id='independent'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("Library note inherited origin project cascade")
	}
}

func TestLibraryAnnotationV89RejectsUnexpectedPredecessor(t *testing.T) {
	for _, ddl := range []string{`ALTER TABLE annotations ADD COLUMN unreviewed TEXT`, `CREATE INDEX unreviewed_annotation_index ON annotations(body)`, `DROP INDEX annotations_project_target_idx; CREATE INDEX annotations_project_target_idx ON annotations(body)`, `DROP INDEX annotations_project_target_idx; CREATE INDEX annotations_project_target_idx ON annotations(project_id COLLATE NOCASE DESC,target_key,label_idx)`, `DROP INDEX annotations_project_target_idx; CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx,lower(body))`,
		`DROP TABLE annotations; CREATE TABLE annotations (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,target_kind TEXT NOT NULL,target_key TEXT NOT NULL,label_idx INTEGER NOT NULL,content_checksum TEXT,body TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP);CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx);CREATE UNIQUE INDEX annotations_target_label_unique_idx ON annotations(project_id,target_key,label_idx)`,
		`DROP TABLE annotations; CREATE TABLE annotations (id TEXT PRIMARY KEY REFERENCES elsewhere,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,target_kind TEXT NOT NULL,target_key TEXT NOT NULL,label_idx INTEGER NOT NULL,content_checksum TEXT,body TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP);CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx);CREATE UNIQUE INDEX annotations_target_label_unique_idx ON annotations(project_id,target_key,label_idx)`} {
		t.Run(ddl, func(t *testing.T) {
			db, _ := newTranscriptWebV78Database(t)
			if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 88); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 89); err == nil {
				t.Fatal("unreviewed predecessor DDL was silently discarded")
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version) FROM workspace_schema_migrations`).Scan(&version); err != nil || version != 88 {
				t.Fatal("failed preflight advanced schema identity")
			}
		})
	}
}
