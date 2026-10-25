package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiteratureCollectionsV84PreservesPopulatedPrefixAndRollsBackDDL(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 83); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(journal) != 83 || len(before["projects"]) == 0 || len(before["literature_items"]) == 0 || len(before["mutation_result_ledger"]) == 0 {
		t.Fatal("fixture must be a populated genuine v83 prefix")
	}
	assertRetained := func(database *sql.DB) {
		t.Helper()
		after := transcriptWebMigrationRows(t, database)
		for table, rows := range before {
			if !reflect.DeepEqual(rows, after[table]) {
				t.Fatalf("v84 rewrote original business rows in %s", table)
			}
		}
		current := literatureIdentityV83Values(t, database, literatureIdentityV83JournalQuery)
		if len(current) < 83 || !reflect.DeepEqual(journal, current[:83]) {
			t.Fatal("v84 rewrote earlier journal identity/timestamp bytes")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_literature_collections_v84 BEFORE INSERT ON workspace_schema_migrations
 WHEN NEW.version=84 BEGIN SELECT RAISE(ABORT,'injected collection marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 84); err == nil || !strings.Contains(err.Error(), "injected collection marker failure") {
		t.Fatalf("expected actual marker failure, got %v", err)
	}
	assertRetained(db)
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, journal) {
		t.Fatal("failed migration retained a new journal row")
	}
	var targets int
	const targetQuery = `SELECT COUNT(*) FROM sqlite_schema WHERE name IN
 ('literature_collections','literature_collection_items','literature_collections_root_name_idx',
 'literature_collections_owner_parent_order_idx','literature_collection_items_item_idx','literature_collection_items_order_idx')`
	if err := db.QueryRow(targetQuery).Scan(&targets); err != nil || targets != 0 {
		t.Fatalf("failed marker left new DDL: count=%d err=%v", targets, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_literature_collections_v84`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 84); err != nil {
		t.Fatal(err)
	}
	assertRetained(db)
	if err := db.QueryRow(targetQuery).Scan(&targets); err != nil || targets != 6 {
		t.Fatalf("new DDL cohort: count=%d err=%v", targets, err)
	}
	checksum, err := literatureCollectionsV84Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if got := readMigrationJournal(t, path); len(got) != 84 || got[83] != "84:literature-ordinary-collections:"+checksum {
		t.Fatalf("unexpected v84 identity: %+v", got)
	}
	committedJournal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// A separate SQLite connection must recognize the same committed journal.
	// Store-level primary Open/replay is covered by the collection Store case.
	reopened, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), reopened, func() time.Time { return time.Now().Add(time.Hour) }, 84); err != nil {
		t.Fatal(err)
	}
	assertRetained(reopened)
	if got := literatureIdentityV83Values(t, reopened, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, committedJournal) {
		t.Fatal("repeat/reopen changed the original v84 journal")
	}
	var integrity string
	if err := reopened.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	rows, err := reopened.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("new schema has a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
