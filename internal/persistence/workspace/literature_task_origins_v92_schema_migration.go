package workspace

import (
	"context"
	"errors"
)

const (
	literatureTaskOriginsV92CallbackID        = "literature-task-origins-v92-noop"
	literatureTaskOriginsV92PreflightIdentity = "literature-task-origins-v92-empty-target-v1"
	literatureTaskOriginsV92RuleSpec          = "x-science.workspace.literature-task-origins.v92"
)

// Prospective trusted origins; no historical candidate is relabeled as a task.
var literatureTaskOriginsV92Migration = versionedSchemaMigration{
	version: 92, name: "literature-candidate-task-origins",
	identityV2: &schemaMigrationIdentityV2{CallbackID: literatureTaskOriginsV92CallbackID,
		PreflightIdentity: literatureTaskOriginsV92PreflightIdentity, RuleSpec: literatureTaskOriginsV92RuleSpec},
	statements: []string{
		`CREATE TABLE literature_candidate_task_origins (
 owner_user_id TEXT NOT NULL, candidate_id TEXT NOT NULL,
 frame_id TEXT NOT NULL, frame_incarnation_id TEXT NOT NULL,
 root_frame_id TEXT NOT NULL, root_frame_incarnation_id TEXT NOT NULL,
 project_id TEXT NOT NULL, recorded_at TEXT NOT NULL,
 PRIMARY KEY(owner_user_id,candidate_id,frame_incarnation_id),
 FOREIGN KEY(owner_user_id,candidate_id) REFERENCES literature_candidates(owner_user_id,id) ON DELETE CASCADE)`,
	},
}

func preflightLiteratureTaskOriginsV92(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('literature_candidates','projects','frames')`).Scan(&dependencies); err != nil {
		return err
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_candidate_task_origins'`).Scan(&targets); err != nil {
		return err
	}
	if dependencies != 3 || targets != 0 {
		return errors.New("Library task origin migration requires original dependencies and empty target")
	}
	return nil
}
