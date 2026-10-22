package server

import (
	"database/sql"
	"testing"
)

// Only private historical test fixtures may remove these prospective tables.
// Other current facts (including original compute jobs and inputs) stay intact.
func dropEmptyReviewedV80LiteratureFixtureTables(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var versions, installed, facts int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&versions, &installed); err != nil || versions != 80 || installed != 80 {
		t.Fatalf("historical reconstruction requires complete reviewed current80 schema: count=%d version=%d err=%v", versions, installed, err)
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM project_literature)+
		(SELECT COUNT(*) FROM literature_attachment_versions)+(SELECT COUNT(*) FROM literature_attachments)+
		(SELECT COUNT(*) FROM literature_items)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical reconstruction requires empty private Library tables: facts=%d err=%v", facts, err)
	}
	for _, statement := range []string{
		`DROP TABLE project_literature`, `DROP TABLE literature_attachment_versions`,
		`DROP TABLE literature_attachments`, `DROP TABLE literature_items`,
	} {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
