package server

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/persistence/workspace"
)

const reviewedHistoricalStudyBatch94Checksum = "eb5d07d602b566b68496c069b4e925729879f2e2ecfe9dc676dff0a6124b8db8"

const reviewedHistoricalStudyBatch94Table = `CREATE TABLE literature_study_batches (
 owner_user_id TEXT PRIMARY KEY,
 batch_id TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9007199254740991),
 checkpoint_json TEXT NOT NULL CHECK(json_valid(checkpoint_json) AND length(CAST(checkpoint_json AS BLOB))<=200000),
 request_token TEXT NOT NULL DEFAULT '',
 request_expires_at INTEGER NOT NULL DEFAULT 0 CHECK(request_expires_at>=0),
 CHECK((request_token='' AND request_expires_at=0) OR (length(request_token)=36 AND request_expires_at>0)))`

// This reversal is restricted to the calling test's private TempDir database.
// Every journal identity and the entire empty v94 object set must match first.
func restoreEmptyHistoricalStudyBatchPredecessor(t *testing.T, tx *sql.Tx) error {
	t.Helper()
	root, err := filepath.EvalSymlinks(filepath.Dir(t.TempDir()))
	if err != nil {
		return err
	}
	var path string
	if err := tx.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("historical batch reconstruction requires the calling test's private TempDir database")
	}
	expected, err := workspace.ExpectedSchemaStatus()
	if err != nil || expected.CurrentVersion != 94 || expected.TargetVersion != 94 || len(expected.Migrations) != 94 {
		return fmt.Errorf("historical batch reconstruction has an unreviewed schema target: %v", err)
	}
	last := expected.Migrations[93]
	if last.Version != 94 || last.Name != "literature-study-batch-checkpoint" || last.Checksum != reviewedHistoricalStudyBatch94Checksum {
		return fmt.Errorf("historical batch reconstruction has an unreviewed batch94 identity")
	}
	rows, err := tx.QueryContext(t.Context(), `SELECT version,name,checksum FROM workspace_schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			_ = rows.Close()
			return err
		}
		if count >= len(expected.Migrations) || expected.Migrations[count].Version != count+1 || version != count+1 || name != expected.Migrations[count].Name || checksum != expected.Migrations[count].Checksum {
			_ = rows.Close()
			return fmt.Errorf("historical batch reconstruction found an unreviewed migration: %d", version)
		}
		count++
	}
	iterationErr := rows.Err()
	if err := rows.Close(); err != nil {
		return err
	}
	if iterationErr != nil || count != 94 {
		return fmt.Errorf("historical batch reconstruction requires the complete94 journal: count=%d err=%v", count, iterationErr)
	}
	var facts, objects, unknown int
	var definition string
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM literature_study_batches`).Scan(&facts); err != nil {
		return err
	}
	if facts != 0 {
		return fmt.Errorf("historical batch reconstruction requires an empty private table: count=%d", facts)
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*),SUM(NOT ((type='table' AND name='literature_study_batches') OR
		(type='index' AND name IN ('sqlite_autoindex_literature_study_batches_1','sqlite_autoindex_literature_study_batches_2') AND sql IS NULL)))
		FROM sqlite_schema WHERE tbl_name='literature_study_batches'`).Scan(&objects, &unknown); err != nil {
		return err
	}
	if err := tx.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_schema WHERE type='table' AND name='literature_study_batches'`).Scan(&definition); err != nil {
		return err
	}
	if objects != 3 || unknown != 0 || strings.Join(strings.Fields(definition), " ") != strings.Join(strings.Fields(reviewedHistoricalStudyBatch94Table), " ") {
		return fmt.Errorf("historical batch reconstruction found unreviewed DDL or child objects")
	}
	if _, err := tx.ExecContext(t.Context(), `DROP TABLE literature_study_batches`); err != nil {
		return err
	}
	result, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_schema_migrations WHERE version=94 AND name=? AND checksum=?`, last.Name, last.Checksum)
	if err != nil {
		return err
	}
	if removed, err := result.RowsAffected(); err != nil || removed != 1 {
		return fmt.Errorf("historical batch reconstruction did not remove exactly the reviewed94 tail: count=%d err=%v", removed, err)
	}
	return nil
}

func TestHistoricalStudyBatchFixtureRejectsRetainedOrUnreviewedState(t *testing.T) {
	for _, test := range []struct{ name, change, want string }{
		{"populated", `INSERT INTO literature_study_batches(owner_user_id,batch_id,revision,checkpoint_json) VALUES('fixture-owner','retained-batch',1,'{"source":"retained"}')`, "empty private table"},
		{"unknown_index", `CREATE INDEX unreviewed_batch_index ON literature_study_batches(revision)`, "unreviewed DDL or child objects"},
		{"unknown_trigger", `CREATE TRIGGER unreviewed_batch_trigger AFTER INSERT ON literature_study_batches BEGIN SELECT 1; END`, "unreviewed DDL or child objects"},
		{"unknown_ddl", `ALTER TABLE literature_study_batches ADD COLUMN unreviewed TEXT`, "unreviewed DDL or child objects"},
		{"changed_journal", `UPDATE workspace_schema_migrations SET checksum='unreviewed' WHERE version=1`, "unreviewed migration"},
		{"missing_journal", `DELETE FROM workspace_schema_migrations WHERE version=94`, "complete94 journal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "historical-batch.db")
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
			if _, err := tx.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at)
				VALUES('retained-source','fixture-owner','Retained source','{"abstract":"Retained evidence"}','2026-11-07','2026-11-07')`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(test.change); err != nil {
				t.Fatal(err)
			}
			snapshot := func() string {
				t.Helper()
				var value string
				if err := tx.QueryRow(`SELECT json_array(
					(SELECT json_group_array(json_array(type,name,tbl_name,sql)) FROM (SELECT * FROM sqlite_schema ORDER BY name)),
					(SELECT json_group_array(json_array(version,name,checksum,applied_at)) FROM (SELECT * FROM workspace_schema_migrations ORDER BY version)),
					(SELECT json_group_array(json_array(owner_user_id,batch_id,revision,checkpoint_json,request_token,request_expires_at)) FROM literature_study_batches),
					(SELECT json_group_array(json_array(id,owner_user_id,title,metadata_revision,metadata_json,deleted_at,created_at,updated_at,reading_status)) FROM literature_items))`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := snapshot()
			if err := restoreEmptyHistoricalStudyBatchPredecessor(t, tx); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("retained or unreviewed batch fixture was not rejected for %q: %v", test.want, err)
			}
			if after := snapshot(); after != before {
				t.Fatal("rejected batch reconstruction changed source, schema or migration journal")
			}
		})
	}
}
