package server

import (
	"database/sql"
	"testing"

	"x-science/internal/persistence/workspace"
)

// Only private historical test fixtures may remove these prospective tables.
// Other current facts (including original compute jobs and inputs) stay intact.
func dropEmptyReviewedV81MemoryAndLiteratureFixtureTables(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var versions, installed, facts int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&versions, &installed); err != nil || versions != 81 || installed != 81 {
		t.Fatalf("historical reconstruction requires complete reviewed current81 schema: count=%d version=%d err=%v", versions, installed, err)
	}
	// This explicit prospective removal must be reviewed again for a later
	// schema. Merely accepting the new maximum would leave newer objects behind
	// while the callers reconstruct their real v74/v76 predecessor receipts.
	expected, err := workspace.ExpectedSchemaStatus()
	if err != nil || expected.TargetVersion != 81 || len(expected.Migrations) != 81 {
		t.Fatalf("historical reconstruction has an unreviewed schema target: %+v err=%v", expected, err)
	}
	want := expected.Migrations[len(expected.Migrations)-1]
	var name, checksum string
	if err := tx.QueryRowContext(t.Context(), `SELECT name,checksum FROM workspace_schema_migrations WHERE version=81`).Scan(&name, &checksum); err != nil || name != want.Name || checksum != want.Checksum {
		t.Fatalf("historical reconstruction requires the original81 identity: name=%s checksum=%s err=%v", name, checksum, err)
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM project_literature)+
		(SELECT COUNT(*) FROM literature_attachment_versions)+(SELECT COUNT(*) FROM literature_attachments)+
		(SELECT COUNT(*) FROM literature_items)+(SELECT COUNT(*) FROM memory_extraction_receipts)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical reconstruction requires empty private Memory/Library tables: facts=%d err=%v", facts, err)
	}
	for _, statement := range []string{
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
