package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/persistence/workspace"
)

// Only the reviewed empty child may be removed from a private historical test
// database. Installed workspaces always use the original forward migration.
func restoreEmptyHistoricalStudyPredecessor(ctx context.Context, tx *sql.Tx) error {
	var facts, objects int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM literature_study_snapshots`).Scan(&facts); err != nil {
		return err
	}
	if facts != 0 {
		return fmt.Errorf("historical study reconstruction requires an empty private table: count=%d", facts)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE tbl_name='literature_study_snapshots' AND sql IS NOT NULL`).Scan(&objects); err != nil {
		return err
	}
	var definition string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='literature_study_snapshots'`).Scan(&definition); err != nil {
		return err
	}
	if objects != 1 || strings.Join(strings.Fields(definition), " ") != strings.Join(strings.Fields(reviewedHistoricalStudy91Table), " ") {
		return fmt.Errorf("historical study reconstruction found an unreviewed table or child object")
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE literature_study_snapshots`)
	return err
}

const reviewedHistoricalStudy91Table = `CREATE TABLE literature_study_snapshots (
 owner_user_id TEXT NOT NULL,
 item_id TEXT NOT NULL REFERENCES literature_items(id) ON DELETE CASCADE,
 language TEXT NOT NULL CHECK(length(language) BETWEEN 1 AND 64),
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9007199254740991),
 source_key TEXT NOT NULL CHECK(length(source_key) BETWEEN 1 AND 4096),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=40000),
 PRIMARY KEY(owner_user_id,item_id,language))`

func TestHistoricalStudyFixtureRestoresOnlyEmptyReviewedPredecessor(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "historical-study.db")
			store, err := workspace.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			var retainedSource string
			if populated {
				created, err := store.CreateManualLiterature(t.Context(), "fixture-owner", workspace.LiteratureManualCreate{
					OperationID: "00000000-0000-4000-8000-000000000091", Title: "Synthetic retained evidence",
					Metadata: map[string]any{"abstract": "Retained synthetic evidence"},
				})
				if err != nil {
					t.Fatal(err)
				}
				source, view, err := store.ReadLiteratureStudy(t.Context(), "fixture-owner", created.Item.ID, "en-US")
				if err != nil {
					t.Fatal(err)
				}
				question := "Retained synthetic question"
				_, err = store.PublishLiteratureStudy(t.Context(), "fixture-owner", created.Item.ID,
					workspace.LiteratureStudyGenerate{OperationID: "00000000-0000-4000-8000-000000000092", Language: "en-US", ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision},
					workspace.LiteratureStudySnapshot{SourceKey: source.Key, Coverage: "abstract", Model: "synthetic-model", Sections: workspace.LiteratureStudySections{Question: &question}})
				if err != nil {
					t.Fatal(err)
				}
				retainedSource = source.Key
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
			if !populated {
				if err := restoreEmptyHistoricalTaskOriginPredecessor(t.Context(), tx); err != nil {
					t.Fatal(err)
				}
			}
			err = restoreEmptyHistoricalStudyPredecessor(t.Context(), tx)
			if populated {
				if err == nil || !strings.Contains(err.Error(), "empty private table") {
					t.Fatalf("populated Study reconstruction was not rejected: %v", err)
				}
				var source, question string
				if err := tx.QueryRow(`SELECT source_key,json_extract(snapshot_json,'$.sections.question') FROM literature_study_snapshots`).Scan(&source, &question); err != nil || source != retainedSource || question != "Retained synthetic question" {
					t.Fatalf("rejected reconstruction changed retained Study: source=%q question=%q err=%v", source, question, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`DELETE FROM workspace_schema_migrations WHERE version IN (91,92)`); err != nil {
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
			expected, expectedErr := workspace.ExpectedSchemaStatus()
			if err != nil || expectedErr != nil || status.CurrentVersion != expected.TargetVersion || len(status.Migrations) != len(expected.Migrations) {
				t.Fatalf("Study predecessor did not migrate back: status=%+v err=%v expectedErr=%v", status, err, expectedErr)
			}
		})
	}
}
