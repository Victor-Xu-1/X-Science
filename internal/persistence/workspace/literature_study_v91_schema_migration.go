package workspace

import (
	"context"
	"errors"
)

const (
	literatureStudyV91CallbackID        = "literature-study-v91-noop"
	literatureStudyV91PreflightIdentity = "literature-study-v91-empty-target-v1"
	literatureStudyV91RuleSpec          = "x-science.workspace.literature-study.v91"
)

var literatureStudyV91Migration = versionedSchemaMigration{
	version: 91, name: "literature-study-snapshots",
	identityV2: &schemaMigrationIdentityV2{CallbackID: literatureStudyV91CallbackID, PreflightIdentity: literatureStudyV91PreflightIdentity, RuleSpec: literatureStudyV91RuleSpec},
	statements: []string{`CREATE TABLE literature_study_snapshots (
 owner_user_id TEXT NOT NULL,
 item_id TEXT NOT NULL REFERENCES literature_items(id) ON DELETE CASCADE,
 language TEXT NOT NULL CHECK(length(language) BETWEEN 1 AND 64),
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9007199254740991),
 source_key TEXT NOT NULL CHECK(length(source_key) BETWEEN 1 AND 4096),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND length(CAST(snapshot_json AS BLOB))<=40000),
 PRIMARY KEY(owner_user_id,item_id,language))`},
}

func preflightLiteratureStudyV91(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&dependencies); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_study_snapshots'`).Scan(&targets); err != nil {
		return err
	}
	if dependencies != 1 || targets != 0 {
		return errors.New("literature study migration requires original dependencies and empty target")
	}
	return nil
}
