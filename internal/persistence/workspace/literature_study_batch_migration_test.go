package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestLiteratureStudyBatchMigration94Atomic(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 93); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_batch_marker BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=94 BEGIN SELECT RAISE(ABORT,'injected batch marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 94); err == nil || !strings.Contains(err.Error(), "injected batch marker failure") {
		t.Fatal("missing atomic rollback", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_study_batches'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial batch DDL", count, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_batch_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 94); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 94); err != nil {
		t.Fatal("migration replay", err)
	}
}
