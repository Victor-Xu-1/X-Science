package workspace

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiteratureTaskOriginsV92PreservesPrefixAndRollsBackDDL(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 91); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	assertPrefix := func() {
		t.Helper()
		after := transcriptWebMigrationRows(t, db)
		for name, rows := range before {
			if !reflect.DeepEqual(rows, after[name]) {
				t.Fatalf("old rows changed in %s", name)
			}
		}
		got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
		if len(got) < 91 || !reflect.DeepEqual(journal, got[:91]) {
			t.Fatal("original90/91 prefix changed")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_origin_marker BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=92 BEGIN SELECT RAISE(ABORT,'injected origin marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err == nil || !strings.Contains(err.Error(), "injected origin marker failure") {
		t.Fatalf("failure=%v", err)
	}
	assertPrefix()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_candidate_task_origins'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback table=%d %v", count, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_origin_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err != nil {
		t.Fatal(err)
	}
	assertPrefix()
	if err := db.QueryRow(`SELECT COUNT(*) FROM literature_candidate_task_origins`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invented origin=%d %v", count, err)
	}
	committed := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(committed, literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)) {
		t.Fatal("repeat changed receipt")
	}
}

func TestLiteratureTaskOriginsV92RejectsUnknownTargetAndForeignCandidate(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 91); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE literature_candidate_task_origins(unreviewed TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err == nil {
		t.Fatal("unknown target accepted")
	}
	if _, err := db.Exec(`DROP TABLE literature_candidate_task_origins`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 92); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,state,created_at,updated_at) VALUES('c','alice','test','source','Reference','{}','pending','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_candidate_task_origins VALUES('bob','c','f','fi','r','ri','p','now')`); err == nil {
		t.Fatal("foreign candidate origin accepted")
	}
}
