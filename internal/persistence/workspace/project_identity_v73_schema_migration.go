package workspace

import (
	"context"
	"errors"
)

const (
	projectIdentityV73CallbackID        = "project-identity-history-v73-noop"
	projectIdentityV73PreflightIdentity = "project-identity-history-v73-projects-v1"
	projectIdentityV73RuleSpec          = "x-science.workspace.project-identity-history.v73"
)

// IDs name durable entities, not reusable project slots. Keeping their history
// inside the canonical database makes registration atomic with every INSERT,
// including direct SQL and caller-owned outbox/preparation transactions. No FK
// to projects is intentional: hard deletion must not delete identity history.
var projectIdentityV73Migration = versionedSchemaMigration{
	version: 73,
	name:    "project-identity-history",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        projectIdentityV73CallbackID,
		PreflightIdentity: projectIdentityV73PreflightIdentity,
		RuleSpec:          projectIdentityV73RuleSpec,
	},
	statements: []string{
		`CREATE TABLE project_id_history (id TEXT PRIMARY KEY NOT NULL)`,
		`INSERT INTO project_id_history(id) SELECT id FROM projects`,
		`CREATE TRIGGER projects_identity_guard BEFORE INSERT ON projects
		WHEN EXISTS(SELECT 1 FROM project_id_history WHERE id=NEW.id)
		BEGIN
			SELECT RAISE(ABORT, 'project identity has already been used; create a new project id');
		END`,
		`CREATE TRIGGER projects_identity_register AFTER INSERT ON projects
		BEGIN
			INSERT INTO project_id_history(id) VALUES(NEW.id);
		END`,
		`CREATE TRIGGER projects_identity_immutable BEFORE UPDATE OF id ON projects
		WHEN NEW.id IS NOT OLD.id
		BEGIN
			SELECT RAISE(ABORT, 'project identity is immutable');
		END`,
	},
}

func preflightProjectIdentityV73(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var count int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='id' AND pk=1`).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("project identity migration requires canonical project primary keys")
	}
	return nil
}
