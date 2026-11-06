package workspace

import (
	"context"
	"errors"
)

const (
	literatureStudyBatchV94CallbackID        = "literature-study-batch-v94-noop"
	literatureStudyBatchV94PreflightIdentity = "literature-study-batch-v94-empty-target-v1"
	literatureStudyBatchV94RuleSpec          = "x-science.workspace.literature-study-batch.v94"
)

var literatureStudyBatchV94Migration = versionedSchemaMigration{
	version: 94, name: "literature-study-batch-checkpoint",
	identityV2: &schemaMigrationIdentityV2{CallbackID: literatureStudyBatchV94CallbackID, PreflightIdentity: literatureStudyBatchV94PreflightIdentity, RuleSpec: literatureStudyBatchV94RuleSpec},
	statements: []string{`CREATE TABLE literature_study_batches (
 owner_user_id TEXT PRIMARY KEY,
 batch_id TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9007199254740991),
 checkpoint_json TEXT NOT NULL CHECK(json_valid(checkpoint_json) AND length(CAST(checkpoint_json AS BLOB))<=200000),
 request_token TEXT NOT NULL DEFAULT '',
 request_expires_at INTEGER NOT NULL DEFAULT 0 CHECK(request_expires_at>=0),
 CHECK((request_token='' AND request_expires_at=0) OR (length(request_token)=36 AND request_expires_at>0)))`},
}

func preflightLiteratureStudyBatchV94(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('literature_items','literature_study_snapshots')`).Scan(&dependencies); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_study_batches'`).Scan(&targets); err != nil {
		return err
	}
	if dependencies != 2 || targets != 0 {
		return errors.New("literature batch migration requires original dependencies and empty target")
	}
	return nil
}
