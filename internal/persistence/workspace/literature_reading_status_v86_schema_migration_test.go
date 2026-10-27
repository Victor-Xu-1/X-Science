package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

const readingStatusV86OriginalItemColumns = `SELECT printf('%s:%s:%s:%d:%s:%s:%s:%s',
 hex(id),hex(owner_user_id),hex(title),metadata_revision,hex(metadata_json),
 CASE WHEN deleted_at IS NULL THEN 'null' ELSE 'text:'||hex(deleted_at) END,
 hex(CAST(created_at AS BLOB)),hex(CAST(updated_at AS BLOB))) FROM literature_items ORDER BY id`

func TestLiteratureReadingStatusV86PreservesPopulatedV85AndRollsBackColumn(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	// Require the actual registered v85 predecessor. This draft must not be run
	// against main67 or manufacture a schema while v85 is still pending merge.
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 85); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	if _, err := db.Exec(`INSERT INTO literature_collections(id,owner_user_id,name,name_key,created_at,updated_at)
 VALUES('reading-retained-collection','owner-v79','Retained','retained','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_collection_items(collection_id,item_id,sort_order,added_at)
 VALUES('reading-retained-collection','legacy-reader-floor-item',0,'2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	const evidence = `{"doi":"10.1234/legacy","checked_at":"2026-01-01T00:00:00Z","notices":[]}`
	if _, err := db.Exec(`INSERT INTO literature_publication_checks(item_id,source_doi,snapshot_json)
 VALUES('legacy-reader-floor-item','10.1234/legacy',?)`, evidence); err != nil {
		t.Fatal(err)
	}
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	items := literatureIdentityV83Values(t, db, readingStatusV86OriginalItemColumns)
	if len(journal) != 85 || len(items) == 0 || len(before["projects"]) == 0 || len(before["mutation_result_ledger"]) == 0 ||
		len(before["literature_collections"]) == 0 || len(before["literature_collection_items"]) == 0 || len(before["literature_publication_checks"]) == 0 {
		t.Fatal("fixture must contain a populated, genuine v85 prefix")
	}
	// All original item columns are checked separately: SELECT * legitimately
	// gains one status field after ALTER TABLE, while prior bytes must not change.
	delete(before, "literature_items")
	assertPreserved := func(database *sql.DB) {
		t.Helper()
		after := transcriptWebMigrationRows(t, database)
		delete(after, "literature_items")
		if !reflect.DeepEqual(after, before) || !reflect.DeepEqual(literatureIdentityV83Values(t, database, readingStatusV86OriginalItemColumns), items) {
			t.Fatal("reading status migration changed an original business row or legacy ACK")
		}
		current := literatureIdentityV83Values(t, database, literatureIdentityV83JournalQuery)
		if len(current) < 85 || !reflect.DeepEqual(current[:85], journal) {
			t.Fatal("reading status migration rewrote original journal identity/timestamp bytes")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_reading_status_v86_marker BEFORE INSERT ON workspace_schema_migrations
 WHEN NEW.version=86 BEGIN SELECT RAISE(ABORT,'injected reading status marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 86); err == nil || !strings.Contains(err.Error(), "injected reading status marker failure") {
		t.Fatalf("expected actual v86 marker failure: %v", err)
	}
	assertPreserved(db)
	if current := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(current, journal) {
		t.Fatal("failed migration retained a v86 journal marker")
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
	assertPreserved(db)
	var columns int
	const columnQuery = `SELECT COUNT(*) FROM pragma_table_info('literature_items') WHERE name='reading_status'`
	if err := db.QueryRow(columnQuery).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("failed marker left its column after reopen: %d %v", columns, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_reading_status_v86_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 86); err != nil {
		t.Fatal(err)
	}
	assertPreserved(db)
	if err := db.QueryRow(columnQuery).Scan(&columns); err != nil || columns != 1 {
		t.Fatalf("reading status column not committed: %d %v", columns, err)
	}
	var status string
	if err := db.QueryRow(`SELECT reading_status FROM literature_items WHERE id='legacy-reader-floor-item'`).Scan(&status); err != nil || status != "unread" {
		t.Fatalf("existing reference did not default to unread: %q %v", status, err)
	}
	for _, value := range []any{"finished", nil} {
		if _, err := db.Exec(`UPDATE literature_items SET reading_status=? WHERE id='legacy-reader-floor-item'`, value); err == nil {
			t.Fatalf("reading status enum/null constraint accepted %#v", value)
		}
	}
	assertPreserved(db)
	if err := db.QueryRow(`SELECT reading_status FROM literature_items WHERE id='legacy-reader-floor-item'`).Scan(&status); err != nil || status != "unread" {
		t.Fatal("rejected status update changed the default", status, err)
	}
	checksum, err := literatureReadingStatusV86Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if current := readMigrationJournal(t, path); len(current) != 86 || current[85] != "86:literature-reading-status:"+checksum {
		t.Fatalf("wrong v86 journal identity: %+v", current)
	}
	committed := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openDatabase()
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, func() time.Time { return time.Now().Add(time.Hour) }, 86); err != nil {
		t.Fatal(err)
	}
	assertPreserved(db)
	if current := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(current, committed) {
		t.Fatal("reopen/repeat changed the committed journal")
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
		t.Fatal("reading status migration introduced a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
