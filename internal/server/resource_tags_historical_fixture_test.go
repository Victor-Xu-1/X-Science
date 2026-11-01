package server

import (
	"database/sql"
	"testing"
)

// This is private historical-fixture reconstruction, never a runtime downgrade.
// Even an initialized empty owner's catalog is retained evidence and forbids it.
func dropEmptyReviewedResourceTagFixtureObjects(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var facts int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM resource_tag_catalog_state)+(SELECT COUNT(*) FROM resource_tags)+(SELECT COUNT(*) FROM resource_tag_assignments)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical reconstruction requires empty private resource tags: facts=%d err=%v", facts, err)
	}
	for _, statement := range []string{
		`DROP TRIGGER resource_tags_literature_delete`,
		`DROP TABLE resource_tag_assignments`,
		`DROP TABLE resource_tags`,
		`DROP TABLE resource_tag_catalog_state`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
