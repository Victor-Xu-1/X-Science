package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

const reviewedHistoricalTaskOrigin92Table = `CREATE TABLE literature_candidate_task_origins (
 owner_user_id TEXT NOT NULL, candidate_id TEXT NOT NULL,
 frame_id TEXT NOT NULL, frame_incarnation_id TEXT NOT NULL,
 root_frame_id TEXT NOT NULL, root_frame_incarnation_id TEXT NOT NULL,
 project_id TEXT NOT NULL, recorded_at TEXT NOT NULL,
 PRIMARY KEY(owner_user_id,candidate_id,frame_incarnation_id),
 FOREIGN KEY(owner_user_id,candidate_id) REFERENCES literature_candidates(owner_user_id,id) ON DELETE CASCADE)`

// Only empty private test fixtures may reconstruct this exact predecessor.
func restoreEmptyHistoricalTaskOriginPredecessor(ctx context.Context, tx *sql.Tx) error {
	var rows, objects int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM literature_candidate_task_origins`).Scan(&rows); err != nil {
		return err
	}
	if rows != 0 {
		return fmt.Errorf("historical task origin reconstruction requires an empty private table: count=%d", rows)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE tbl_name='literature_candidate_task_origins' AND sql IS NOT NULL`).Scan(&objects); err != nil {
		return err
	}
	var definition string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='literature_candidate_task_origins'`).Scan(&definition); err != nil {
		return err
	}
	if objects != 1 || strings.Join(strings.Fields(definition), " ") != strings.Join(strings.Fields(reviewedHistoricalTaskOrigin92Table), " ") {
		return fmt.Errorf("historical task origin reconstruction found unreviewed DDL")
	}
	_, err := tx.ExecContext(ctx, `DROP TABLE literature_candidate_task_origins`)
	return err
}

func TestHistoricalTaskOriginFixtureRestoresOnlyEmptyReviewedPredecessor(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "historical-origin.sqlite")
			store, err := workspace.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
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
			if err := restoreEmptyHistoricalStudyBatchPredecessor(t, tx); err != nil {
				t.Fatal(err)
			}
			if populated {
				if _, err = tx.Exec(`INSERT INTO literature_candidates(id,owner_user_id,provider,external_id,title,metadata_json,state,created_at,updated_at) VALUES('historical-candidate','owner','fixture','record','Retained origin','{}','pending','2026-11-04','2026-11-04')`); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(`INSERT INTO literature_candidate_task_origins VALUES('owner','historical-candidate','frame','frame-inc','root','root-inc','project','2026-11-04T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
			}
			err = restoreEmptyHistoricalTaskOriginPredecessor(t.Context(), tx)
			if populated {
				if err == nil || !strings.Contains(err.Error(), "empty private table") {
					t.Fatalf("populated origin not protected: %v", err)
				}
				var frame string
				if err = tx.QueryRow(`SELECT frame_incarnation_id FROM literature_candidate_task_origins`).Scan(&frame); err != nil || frame != "frame-inc" {
					t.Fatalf("origin lost=%q %v", frame, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := restoreEmptyHistoricalProjectionState(t.Context(), tx, 79); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(`DELETE FROM workspace_schema_migrations WHERE version IN (92,93)`); err != nil {
				t.Fatal(err)
			}
			if err := requireHistoricalFixturePrefix(t.Context(), tx, 91); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := workspace.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			status, err := reopened.SchemaStatus(t.Context())
			expected, expectedErr := workspace.ExpectedSchemaStatus()
			if err != nil || expectedErr != nil || status.CurrentVersion != expected.TargetVersion || len(status.Migrations) != len(expected.Migrations) {
				t.Fatalf("status=%#v err=%v expectedErr=%v", status, err, expectedErr)
			}
		})
	}
}
