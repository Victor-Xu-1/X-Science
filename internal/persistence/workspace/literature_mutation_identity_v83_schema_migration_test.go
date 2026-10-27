package workspace

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiteratureMutationIdentityV83JournalOnlyRollbackAndIdempotence(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 82); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := readMigrationJournal(t, path)
	rawJournal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	ddl := literatureIdentityV83Values(t, db, literatureIdentityV83SchemaQuery)
	if len(journal) != 82 || len(rawJournal) != 82 || len(before["projects"]) == 0 ||
		len(before["literature_items"]) == 0 || len(before["mutation_result_ledger"]) == 0 {
		t.Fatal("fixture must have the actual v82 prefix and nonempty business/legacy rows")
	}
	assertPreserved := func() {
		t.Helper()
		if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) {
			t.Fatal("reader floor changed business rows or raw legacy ACK")
		}
		if !reflect.DeepEqual(ddl, literatureIdentityV83Values(t, db, literatureIdentityV83SchemaQuery)) {
			t.Fatal("reader floor changed business or journal DDL")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_literature_identity_v83 BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=83 BEGIN SELECT RAISE(ABORT,'injected Library reader floor failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 83); err == nil ||
		!strings.Contains(err.Error(), "injected Library reader floor failure") {
		t.Fatalf("expected marker insertion rollback, got %v", err)
	}
	assertPreserved()
	if !reflect.DeepEqual(journal, readMigrationJournal(t, path)) ||
		!reflect.DeepEqual(rawJournal, literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)) {
		t.Fatal("failed marker insertion changed journal identity or stored timestamp bytes")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_literature_identity_v83`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 83); err != nil {
		t.Fatal(err)
	}
	checksum, err := literatureMutationIdentityV83Migration.validatedChecksum()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Library reader floor migration: version=83 name=%s checksum=%s", literatureMutationIdentityV83Migration.name, checksum)
	canonical, err := literatureMutationIdentityV83Migration.canonicalChecksum()
	if err != nil || checksum != canonical || len(literatureMutationIdentityV83Migration.statements) != 0 {
		t.Fatalf("journal-only canonical identity changed: checksum=%s canonical=%s err=%v", checksum, canonical, err)
	}
	latest := readMigrationJournal(t, path)
	latestRaw := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(latest) != 83 || !reflect.DeepEqual(journal, latest[:82]) ||
		latest[82] != "83:literature-mutation-identity-reader-floor:"+checksum ||
		len(latestRaw) != 83 || !reflect.DeepEqual(rawJournal, latestRaw[:82]) {
		t.Fatal("migration must add exactly its own marker without changing the v82 prefix")
	}
	assertPreserved()
	later := func() time.Time { return time.Now().Add(time.Hour) }
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, later, 83); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(latest, readMigrationJournal(t, path)) ||
		!reflect.DeepEqual(latestRaw, literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)) {
		t.Fatal("repeated migration changed marker identity or stored timestamp bytes")
	}
	assertPreserved()
}

// This is an isolated historical row-shape fixture, not an assertion that an
// old binary produced this ACK. No migration may decode/re-encode either field.
func seedLiteratureIdentityV83LegacyFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	const metadata = `{"opaque":1e400,"nested":{"integer":9007199254740993}}`
	const rawACK = `{
  "schema":"x-science.literature-receipt.v1", "operation_id":"legacy-reader-floor",
  "item":{"id":"legacy-reader-floor-item","title":"Retained metadata","metadata_revision":2,
    "metadata":{"opaque":1e400,"nested":{"integer":9007199254740993}},
    "deleted":false,"project_ids":[],"attachments":[]}
}`
	input := LiteraturePatch{OperationID: "legacy-reader-floor", ExpectedRevision: 1,
		Metadata: map[string]any{"opaque": json.Number("1e400"), "nested": map[string]any{"integer": json.Number("9007199254740993")}}}
	legacyHash, err := mutationRequestHash(struct {
		ID    string
		Input LiteraturePatch
	}{"legacy-reader-floor-item", input})
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	if err := store.ensureMutationResultLedgerSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_revision,metadata_json,created_at,updated_at)
		VALUES('legacy-reader-floor-item','owner-v79','Retained metadata',2,?,'2026-01-01T00:00:00Z','2026-01-01T00:00:01Z')`, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mutation_result_ledger(owner_user_id,idempotency_key,operation,request_hash,result_json,created_at)
		VALUES('owner-v79','legacy-reader-floor','literature.patch',?,?,'2026-01-01T00:00:01Z')`, legacyHash, rawACK); err != nil {
		t.Fatal(err)
	}
	var gotHash, gotACK string
	if err := db.QueryRow(`SELECT request_hash,result_json FROM mutation_result_ledger
		WHERE owner_user_id='owner-v79' AND idempotency_key='legacy-reader-floor' AND operation='literature.patch'`).Scan(&gotHash, &gotACK); err != nil ||
		gotHash != legacyHash || len(gotHash) != 64 || gotACK != rawACK {
		t.Fatalf("legacy bytes were not seeded exactly: %v", err)
	}
}

const literatureIdentityV83JournalQuery = `SELECT printf('%d:%s:%s:%s',version,hex(name),hex(checksum),hex(CAST(applied_at AS BLOB)))
	FROM workspace_schema_migrations ORDER BY version`

const literatureIdentityV83SchemaQuery = `SELECT printf('%s:%s:%s:%s',type,hex(name),hex(tbl_name),hex(COALESCE(sql,'')))
	FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND name!='fail_literature_identity_v83' ORDER BY type,name`

func literatureIdentityV83Values(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}
