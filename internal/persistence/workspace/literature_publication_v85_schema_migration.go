package workspace

import (
	"context"
	"errors"
)

const (
	literaturePublicationV85CallbackID        = "literature-publication-check-v85-noop"
	literaturePublicationV85PreflightIdentity = "literature-publication-check-v85-empty-target-v1"
	literaturePublicationV85RuleSpec          = "x-science.workspace.literature-publication-check.v85"
)

var literaturePublicationV85Migration = versionedSchemaMigration{
	version: 85,
	name:    "literature-publication-check",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        literaturePublicationV85CallbackID,
		PreflightIdentity: literaturePublicationV85PreflightIdentity,
		RuleSpec:          literaturePublicationV85RuleSpec,
	},
	statements: []string{`CREATE TABLE literature_publication_checks (
 item_id TEXT PRIMARY KEY REFERENCES literature_items(id) ON DELETE CASCADE,
 source_doi TEXT NOT NULL CHECK(length(source_doi)>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)))`},
}

func preflightLiteraturePublicationV85(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var dependencies, targets int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='literature_items'`).Scan(&dependencies); err != nil {
		return err
	}
	// The ledger is an existing Store bootstrap concern, created after fresh
	// versioned schema initialization. Only this table's actual FK is needed.
	if dependencies != 1 {
		return errors.New("literature publication dependencies are unavailable")
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='literature_publication_checks'`).Scan(&targets); err != nil {
		return err
	}
	if targets != 0 {
		return errors.New("literature publication migration target is not empty")
	}
	return nil
}
