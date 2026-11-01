package server

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/persistence/workspace"
)

// Only private historical test fixtures may remove these prospective objects.
// Other current facts (including original compute jobs and inputs) stay intact.
func dropEmptyReviewedMemoryAndLiteratureFixtureObjects(t *testing.T, tx *sql.Tx) {
	t.Helper()
	requireReviewedHistoricalFixtureMigrations(t, tx)
	var facts int
	if err := tx.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM project_literature)+
		(SELECT COUNT(*) FROM literature_attachment_versions)+(SELECT COUNT(*) FROM literature_attachments)+
		(SELECT COUNT(*) FROM literature_items)+(SELECT COUNT(*) FROM memory_extraction_receipts)+
		(SELECT COUNT(*) FROM literature_collections)+(SELECT COUNT(*) FROM literature_collection_items)+
		(SELECT COUNT(*) FROM literature_publication_checks)+(SELECT COUNT(*) FROM literature_candidates)+
		(SELECT COUNT(*) FROM literature_candidate_sources)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical reconstruction requires empty private Memory/Library tables: facts=%d err=%v", facts, err)
	}
	var cadenceFacts int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM project_runtime_metadata
		WHERE typeof(memory_completion_ordinal)<>'integer' OR memory_completion_ordinal IS NOT 0`).Scan(&cadenceFacts); err != nil || cadenceFacts != 0 {
		t.Fatalf("historical reconstruction requires unused private v82 cadence: facts=%d err=%v", cadenceFacts, err)
	}
	if err := restoreEmptyHistoricalAnnotationPredecessor(t, tx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		// A v74/v76 predecessor cannot retain the prospective v82 column, even
		// when its default value is unused. Other project metadata stays intact.
		`ALTER TABLE project_runtime_metadata DROP COLUMN memory_completion_ordinal`,
		// The table owns its index and immutability trigger; dropping only a
		// migration receipt would make the subsequent real v81 Open fail.
		`DROP TABLE memory_extraction_receipts`,
		// Remove reviewed v84 children before the Library parent. The emptiness
		// check also covers v85 provider evidence, which has an item foreign key.
		`DROP TABLE literature_candidate_sources`, `DROP TABLE literature_candidates`, `DROP TABLE literature_publication_checks`,
		`DROP TABLE literature_collection_items`, `DROP TABLE literature_collections`,
		`DROP TABLE project_literature`, `DROP TABLE literature_attachment_versions`,
		// Reviewed86's reading_status column belongs to this empty parent and
		// disappears with it; no populated reading state may be projected away.
		`DROP TABLE literature_attachments`, `DROP TABLE literature_items`,
	} {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func requireReviewedHistoricalFixtureMigrations(t *testing.T, tx *sql.Tx) {
	t.Helper()
	const reviewedTarget = 89
	var versions, installed int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),MAX(version) FROM workspace_schema_migrations`).Scan(&versions, &installed); err != nil || versions != reviewedTarget || installed != reviewedTarget {
		t.Fatalf("historical reconstruction requires complete reviewed current%d schema: count=%d version=%d err=%v", reviewedTarget, versions, installed, err)
	}
	// Reviewed83 is a journal-only reader floor: it adds no objects to remove.
	// Reviewed84 adds only the two empty collection tables and table-owned indexes.
	// Reviewed85 adds the empty publication evidence child of literature_items.
	// Reviewed86 adds the status column on the same empty Library parent.
	// Reviewed87 adds empty candidates and sources; the source child is removed first.
	// Reviewed88 is the exact journal-only startup reader floor; it adds no DDL
	// or data to remove from these empty private historical fixtures.
	// Reviewed89 changes annotations; the empty table is restored to its original
	// project-owned shape before callers reconstruct their v74/v76 receipts.
	// These must be removed before reconstructing the earlier Library-free shape.
	// This explicit prospective removal must be reviewed again for a later
	// schema. Merely accepting the new maximum would leave newer objects behind
	// while the callers reconstruct their real v74/v76 predecessor receipts.
	expected, err := workspace.ExpectedSchemaStatus()
	if err != nil || expected.CurrentVersion != reviewedTarget || expected.TargetVersion != reviewedTarget || len(expected.Migrations) != reviewedTarget {
		t.Fatalf("historical reconstruction has an unreviewed schema target: %+v err=%v", expected, err)
	}
	current := expected.Migrations[reviewedTarget-1]
	if current.Version != reviewedTarget || current.Name != "library-annotation-owner" ||
		current.Checksum != "8f8b89fb5c2808bc147541e3df3ce4828f0058c9117ebfa52b710c260a896ca9" {
		t.Fatalf("historical reconstruction requires the reviewed annotation89 identity: %+v", current)
	}
	original88 := expected.Migrations[87]
	if original88.Version != 88 || original88.Name != "kernel-worker-startup-reader-floor" ||
		original88.Checksum != "edab43e8d1d6ae0778bbe6c0b4c48ddfd936e9a72650e52b724af699621ae971" {
		t.Fatalf("historical reconstruction requires the reviewed journal-only startup88 identity: %+v", original88)
	}
	original87 := expected.Migrations[86]
	if original87.Version != 87 || original87.Name != "literature-candidates" || len(original87.Checksum) != 64 {
		t.Fatalf("historical reconstruction requires the reviewed candidate identity: %+v", original87)
	}
	original86 := expected.Migrations[85]
	if original86.Version != 86 || original86.Name != "literature-reading-status" || len(original86.Checksum) != 64 {
		t.Fatalf("historical reconstruction requires the reviewed reading-status identity: %+v", original86)
	}
	var statusColumn int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pragma_table_info('literature_items')
		WHERE name='reading_status' AND type='TEXT' AND "notnull"=1 AND dflt_value=?`, "'unread'").Scan(&statusColumn); err != nil || statusColumn != 1 {
		t.Fatalf("historical reconstruction requires the reviewed status86 column: count=%d err=%v", statusColumn, err)
	}
	original85 := expected.Migrations[84]
	if original85.Version != 85 || original85.Name != "literature-publication-check" || len(original85.Checksum) != 64 {
		t.Fatalf("historical reconstruction requires the reviewed publication85 identity: %+v", original85)
	}
	original84 := expected.Migrations[83]
	if original84.Version != 84 || original84.Name != "literature-ordinary-collections" || len(original84.Checksum) != 64 {
		t.Fatalf("historical reconstruction requires the reviewed collections84 identity: %+v", original84)
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

// This is only a private-fixture reversal. Even a single real annotation must
// remain untouched; installed workspaces always use the forward migration.
func restoreEmptyHistoricalAnnotationPredecessor(t *testing.T, tx *sql.Tx) error {
	t.Helper()
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM annotations`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("historical annotation reconstruction requires an empty private table: count=%d", count)
	}
	expected := map[string]string{
		"table:annotations":                                 reviewedHistoricalAnnotation89Table,
		"index:annotations_project_target_idx":              `CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx)`,
		"index:annotations_target_label_unique_idx":         `CREATE UNIQUE INDEX annotations_target_label_unique_idx ON annotations(project_id,target_key,label_idx)`,
		"index:annotations_library_target_label_unique_idx": `CREATE UNIQUE INDEX annotations_library_target_label_unique_idx ON annotations(library_owner_id,target_key,label_idx) WHERE library_owner_id IS NOT NULL`,
	}
	rows, err := tx.QueryContext(t.Context(), `SELECT type,name,sql FROM sqlite_schema WHERE tbl_name='annotations' AND sql IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind, name, definition string
		if err := rows.Scan(&kind, &name, &definition); err != nil {
			_ = rows.Close()
			return err
		}
		key := kind + ":" + name
		want, ok := expected[key]
		if !ok || strings.Join(strings.Fields(definition), " ") != strings.Join(strings.Fields(want), " ") {
			_ = rows.Close()
			return fmt.Errorf("historical annotation reconstruction found an unreviewed object: %s", key)
		}
		delete(expected, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("historical annotation reconstruction is missing reviewed objects: count=%d", len(expected))
	}
	for _, statement := range []string{
		`DROP TABLE annotations`, historicalAnnotationProjectTable,
		`CREATE INDEX annotations_project_target_idx ON annotations(project_id,target_key,label_idx)`,
		`CREATE UNIQUE INDEX annotations_target_label_unique_idx ON annotations(project_id,target_key,label_idx)`,
	} {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			return err
		}
	}
	return nil
}

const reviewedHistoricalAnnotation89Table = `CREATE TABLE "annotations" (
 id TEXT PRIMARY KEY, project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
 library_owner_id TEXT, target_kind TEXT NOT NULL, target_key TEXT NOT NULL,
 label_idx INTEGER NOT NULL, content_checksum TEXT, body TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP,
 CHECK ((target_kind='literature-attachment-version' AND project_id IS NULL AND library_owner_id IS NOT NULL AND length(library_owner_id)>0 AND target_key GLOB 'litv:litv_*') OR
 (target_kind<>'literature-attachment-version' AND project_id IS NOT NULL AND library_owner_id IS NULL)))`

const historicalAnnotationProjectTable = `CREATE TABLE annotations (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 target_kind TEXT NOT NULL, target_key TEXT NOT NULL, label_idx INTEGER NOT NULL,
 content_checksum TEXT, body TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP)`

func TestHistoricalAnnotationFixtureRestoresOnlyEmptyReviewedPredecessor(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "historical-annotations.db")
			store, err := workspace.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			requireReviewedHistoricalFixtureMigrations(t, tx)
			if populated {
				if _, err := tx.Exec(`INSERT INTO annotations(id,library_owner_id,target_kind,target_key,label_idx,body,created_at)
 VALUES('retained-note','retained-owner','literature-attachment-version','litv:litv_retained',0,'{"text":"Retained note"}','2026-01-01')`); err != nil {
					t.Fatal(err)
				}
				if err := restoreEmptyHistoricalAnnotationPredecessor(t, tx); err == nil || !strings.Contains(err.Error(), "empty private table") {
					t.Fatalf("populated annotation reconstruction was not rejected: %v", err)
				}
				var body, owner string
				if err := tx.QueryRow(`SELECT body,library_owner_id FROM annotations WHERE id='retained-note'`).Scan(&body, &owner); err != nil || body != `{"text":"Retained note"}` || owner != "retained-owner" {
					t.Fatalf("rejected reconstruction changed the annotation: body=%q owner=%q err=%v", body, owner, err)
				}
				return
			}
			if err := restoreEmptyHistoricalAnnotationPredecessor(t, tx); err != nil {
				t.Fatal(err)
			}
			var columns, requiredProject, indexes int
			if err := tx.QueryRow(`SELECT COUNT(*),SUM(name='project_id' AND "notnull"=1) FROM pragma_table_info('annotations')`).Scan(&columns, &requiredProject); err != nil || columns != 9 || requiredProject != 1 {
				t.Fatalf("historical annotations shape: columns=%d required_project=%d err=%v", columns, requiredProject, err)
			}
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('annotations')`).Scan(&indexes); err != nil || indexes != 3 {
				t.Fatalf("historical annotations indexes=%d err=%v", indexes, err)
			}
			if _, err := tx.Exec(`DELETE FROM workspace_schema_migrations WHERE version=89`); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = workspace.Open(path)
			if err != nil {
				t.Fatalf("original forward migration rejected the restored predecessor: %v", err)
			}
			defer store.Close()
			status, err := store.SchemaStatus(t.Context())
			if err != nil || status.CurrentVersion != 89 || len(status.Migrations) != 89 {
				t.Fatalf("restored predecessor did not migrate back: status=%+v err=%v", status, err)
			}
		})
	}
}
