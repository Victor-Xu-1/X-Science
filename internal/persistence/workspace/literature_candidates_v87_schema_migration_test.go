package workspace

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiteratureCandidatesV87PreservesV86AndRollsBack(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 86); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(journal) != 86 || len(before["literature_items"]) == 0 || len(before["mutation_result_ledger"]) == 0 {
		t.Fatal("fixture must contain populated v86 Library and mutation receipts")
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_candidates_v87_marker BEFORE INSERT ON workspace_schema_migrations
 WHEN NEW.version=87 BEGIN SELECT RAISE(ABORT,'injected candidates marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err == nil || !strings.Contains(err.Error(), "injected candidates marker failure") {
		t.Fatalf("expected marker failure, got %v", err)
	}
	if got := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(got, before) {
		t.Fatal("failed migration altered existing rows or retained a candidate table")
	}
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(got, journal) {
		t.Fatal("failed migration retained or altered journal rows")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if got := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(got, before) {
		t.Fatal("failed migration changed predecessor after reopen")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_candidates_v87_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	after := transcriptWebMigrationRows(t, db)
	if rows, exists := after["literature_candidates"]; !exists || len(rows) != 0 {
		t.Fatal("candidate table must be present and empty after migration")
	}
	if rows, exists := after["literature_candidate_sources"]; !exists || len(rows) != 0 {
		t.Fatal("candidate source table must be present and empty after migration")
	}
	delete(after, "literature_candidates")
	delete(after, "literature_candidate_sources")
	if !reflect.DeepEqual(after, before) {
		t.Fatal("migration modified existing Library or unrelated rows")
	}
	current := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(current) != 87 || !reflect.DeepEqual(current[:86], journal) {
		t.Fatal("migration changed predecessor journal")
	}
	checksum, err := literatureCandidatesV87Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	if current := readMigrationJournal(t, path); len(current) != 87 || current[86] != "87:literature-candidates:"+checksum {
		t.Fatalf("incorrect v87 identity: %v", current)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	if again := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(again, current) {
		t.Fatal("repeat migration changed journal")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	var badKeys int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&badKeys); err != nil || badKeys != 0 {
		t.Fatalf("foreign key violations=%d err=%v", badKeys, err)
	}
}

func TestLiteratureCandidatesV87EnforcesIdentityAndDecisionConstraints(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	insert := `INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,doi_key,state,item_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'2026-01-01','2026-01-01')`
	if _, err := db.Exec(insert, "first", "owner-v79", "provider", "one", "First", `{}`, "10.1234/one", "pending", nil); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][]any{
		{"duplicate-source", "owner-v79", "provider", "one", "Duplicate", `{}`, nil, "pending", nil},
		{"duplicate-doi", "owner-v79", "other-provider", "two", "Duplicate", `{}`, "10.1234/one", "pending", nil},
		{"bad-json", "owner-v79", "provider", "three", "Bad", `{`, nil, "pending", nil},
		{"bad-state", "owner-v79", "provider", "four", "Bad", `{}`, nil, "other", nil},
		{"missing-item", "owner-v79", "provider", "five", "Bad", `{}`, nil, "accepted", nil},
		{"pending-item", "owner-v79", "provider", "six", "Bad", `{}`, nil, "pending", "legacy-reader-floor-item"},
		{"missing-ref", "owner-v79", "provider", "seven", "Bad", `{}`, nil, "accepted", "nonexistent"},
	} {
		if _, err := db.Exec(insert, values...); err == nil {
			t.Fatalf("invalid candidate accepted: %v", values)
		}
	}
	for _, values := range [][]any{
		{"other-owner", "other-owner", "provider", "one", "Other", `{}`, "10.1234/one", "pending", nil},
		{"null-doi-one", "owner-v79", "provider", "eight", "No DOI", `{}`, nil, "pending", nil},
		{"null-doi-two", "owner-v79", "provider", "nine", "No DOI", `{}`, nil, "dismissed", nil},
		{"accepted", "owner-v79", "provider", "ten", "Accepted", `{}`, nil, "accepted", "legacy-reader-floor-item"},
	} {
		if _, err := db.Exec(insert, values...); err != nil {
			t.Fatalf("valid candidate rejected: %v: %v", values, err)
		}
	}
}

func TestLiteratureCandidatesV87CursorIndexMatchesOrder(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM pragma_index_info('literature_candidates_owner_state_order_idx') ORDER BY seqno`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"owner_user_id", "state", "id"}; !reflect.DeepEqual(columns, want) {
		t.Fatalf("cursor index columns=%v, want %v", columns, want)
	}
}

func TestLiteratureCandidateSourcesV87EnforcesIdentityAndCascade(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,state,created_at,updated_at)
 VALUES('candidate','owner','provider','first','Paper','{}','pending','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO literature_candidate_sources(owner_user_id,provider,external_id,candidate_id) VALUES(?,?,?,?)`
	if _, err := db.Exec(insert, "owner", "provider", "first", "candidate"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "owner", "provider", "first", "candidate"); err == nil {
		t.Fatal("duplicate owner/provider/external identity accepted")
	}
	if _, err := db.Exec(insert, "owner", "provider", "other", "missing"); err == nil {
		t.Fatal("unknown candidate foreign key accepted")
	}
	if _, err := db.Exec(insert, "foreign-owner", "provider", "other", "candidate"); err == nil {
		t.Fatal("foreign owner could attach a source to another owner's candidate")
	}
	if _, err := db.Exec(insert, "owner", "another-provider", "second", "candidate"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM literature_candidates WHERE id='candidate'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM literature_candidate_sources`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("candidate source cascade retained %d rows: %v", count, err)
	}
}

func TestLiteratureCandidatesV87PreflightRejectsExistingTarget(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 86); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE literature_candidates (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err == nil || !strings.Contains(err.Error(), "target is not empty") {
		t.Fatalf("existing target was accepted: %v", err)
	}
}
