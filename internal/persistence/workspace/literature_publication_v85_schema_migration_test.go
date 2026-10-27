package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiteraturePublicationV85PreservesPopulatedPrefixAndRollsBackDDL(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 84); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	if _, err := db.Exec(`INSERT INTO literature_collections(id,owner_user_id,name,name_key,created_at,updated_at)
 VALUES('publication-retained-collection','owner-v79','Retained collection','retained collection','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_collection_items(collection_id,item_id,sort_order,added_at)
 VALUES('publication-retained-collection','legacy-reader-floor-item',0,'2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(journal) != 84 || len(before["projects"]) == 0 || len(before["literature_items"]) == 0 ||
		len(before["mutation_result_ledger"]) == 0 || len(before["literature_collections"]) == 0 || len(before["literature_collection_items"]) == 0 {
		t.Fatal("fixture must contain a genuine populated v84 prefix")
	}
	assertRetained := func(database *sql.DB) {
		t.Helper()
		after := transcriptWebMigrationRows(t, database)
		for table, rows := range before {
			if !reflect.DeepEqual(rows, after[table]) {
				t.Fatalf("v85 changed prior business rows in %s", table)
			}
		}
		current := literatureIdentityV83Values(t, database, literatureIdentityV83JournalQuery)
		if len(current) < 84 || !reflect.DeepEqual(journal, current[:84]) {
			t.Fatal("v85 changed prior journal identity/timestamp bytes")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_publication_v85_marker BEFORE INSERT ON workspace_schema_migrations
 WHEN NEW.version=85 BEGIN SELECT RAISE(ABORT,'injected publication marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 85); err == nil || !strings.Contains(err.Error(), "injected publication marker failure") {
		t.Fatalf("expected real migration marker failure: %v", err)
	}
	assertRetained(db)
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, journal) {
		t.Fatal("failed v85 retained a journal marker")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	openDatabase := func() *sql.DB {
		t.Helper()
		opened, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path))
		if err != nil {
			t.Fatal(err)
		}
		opened.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = opened.Close() })
		if _, err := opened.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		return opened
	}
	db = openDatabase()
	assertRetained(db)
	var targets int
	const targetQuery = `SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_publication_checks'`
	if err := db.QueryRow(targetQuery).Scan(&targets); err != nil || targets != 0 {
		t.Fatalf("failed marker left DDL after reopen: %d %v", targets, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_publication_v85_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 85); err != nil {
		t.Fatal(err)
	}
	assertRetained(db)
	if err := db.QueryRow(targetQuery).Scan(&targets); err != nil || targets != 1 {
		t.Fatalf("publication table missing after commit: %d %v", targets, err)
	}
	if after := transcriptWebMigrationRows(t, db); len(after) != len(before)+1 || len(after["literature_publication_checks"]) != 0 {
		t.Fatal("migration must add only its empty evidence table")
	}
	checksum, err := literaturePublicationV85Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if got := readMigrationJournal(t, path); len(got) != 85 || got[84] != "85:literature-publication-check:"+checksum {
		t.Fatalf("unexpected v85 migration identity: %+v", got)
	}
	committedJournal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openDatabase()
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, func() time.Time { return time.Now().Add(time.Hour) }, 85); err != nil {
		t.Fatal(err)
	}
	assertRetained(db)
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, committedJournal) {
		t.Fatal("reopen/repeat rewrote v85 journal")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("v85 introduced a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
