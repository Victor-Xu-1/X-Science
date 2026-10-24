package server

import (
	"database/sql"
	"testing"

	"x-science/internal/persistence/workspace"
)

// Only private historical test fixtures may remove these prospective objects.
// Other current facts (including original compute jobs and inputs) stay intact.
func dropEmptyReviewedV82MemoryAndLiteratureFixtureObjects(t *testing.T, tx *sql.Tx) {
	t.Helper()
	requireReviewedV83HistoricalFixtureMigrations(t, tx)
	var facts int
	if err := tx.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM project_literature)+
		(SELECT COUNT(*) FROM literature_attachment_versions)+(SELECT COUNT(*) FROM literature_attachments)+
		(SELECT COUNT(*) FROM literature_items)+(SELECT COUNT(*) FROM memory_extraction_receipts)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical reconstruction requires empty private Memory/Library tables: facts=%d err=%v", facts, err)
	}
	var cadenceFacts int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_runtime_metadata
		WHERE typeof(memory_completion_ordinal)<>'integer' OR memory_completion_ordinal IS NOT 0`).Scan(&cadenceFacts); err != nil || cadenceFacts != 0 {
		t.Fatalf("historical reconstruction requires unused private v82 cadence: facts=%d err=%v", cadenceFacts, err)
	}
	for _, statement := range []string{
		// A v74/v76 predecessor cannot retain the prospective v82 column, even
		// when its default value is unused. Other project metadata stays intact.
		`ALTER TABLE project_runtime_metadata DROP COLUMN memory_completion_ordinal`,
		// The table owns its index and immutability trigger; dropping only a
		// migration receipt would make the subsequent real v81 Open fail.
		`DROP TABLE memory_extraction_receipts`,
		`DROP TABLE project_literature`, `DROP TABLE literature_attachment_versions`,
		`DROP TABLE literature_attachments`, `DROP TABLE literature_items`,
	} {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func requireReviewedV83HistoricalFixtureMigrations(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var versions, installed int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&versions, &installed); err != nil || versions != 83 || installed != 83 {
		t.Fatalf("historical reconstruction requires complete reviewed current83 schema: count=%d version=%d err=%v", versions, installed, err)
	}
	// Reviewed83 is a journal-only reader floor: it adds no objects to remove.
	// The prospective Memory/Library object removal is therefore still v82.
	// This explicit prospective removal must be reviewed again for a later
	// schema. Merely accepting the new maximum would leave newer objects behind
	// while the callers reconstruct their real v74/v76 predecessor receipts.
	expected, err := workspace.ExpectedSchemaStatus()
	if err != nil || expected.CurrentVersion != 83 || expected.TargetVersion != 83 || len(expected.Migrations) != 83 {
		t.Fatalf("historical reconstruction has an unreviewed schema target: %+v err=%v", expected, err)
	}
	original83 := expected.Migrations[82]
	if original83.Version != 83 || original83.Name != "literature-mutation-identity-reader-floor" ||
		original83.Checksum != "cdcc14361344bd4be6a64413ad21a2b41ab9bdc43b8607447b23ef1ce3282613" {
		t.Fatalf("historical reconstruction requires the reviewed journal-only83 identity: %+v", original83)
	}
	original81 := expected.Migrations[80]
	if original81.Version != 81 || original81.Name != "atomic-memory-extraction-receipts" ||
		original81.Checksum != "1377681f2842d72d9ed38a957fd052b8756793940357b3436bff8e14ccba71c5" {
		t.Fatalf("historical reconstruction requires the original81 identity: %+v", original81)
	}
	rows, err := tx.QueryContext(t.Context(), `SELECT version,name,checksum FROM workspace_schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			t.Fatal(err)
		}
		if count >= len(expected.Migrations) {
			t.Fatal("historical reconstruction contains an unexpected migration")
		}
		want := expected.Migrations[count]
		if want.Version != count+1 || version != want.Version || name != want.Name || checksum != want.Checksum {
			t.Fatalf("historical reconstruction migration %d does not match reviewed identity: version=%d name=%s checksum=%s", count+1, version, name, checksum)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(expected.Migrations) {
		t.Fatalf("historical reconstruction has an incomplete reviewed prefix: count=%d", count)
	}
}
