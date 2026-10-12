package workspace

import (
	"context"
	"errors"
)

const (
	dataLifecycleIndexesV74CallbackID        = "data-lifecycle-snapshot-indexes-v74-noop"
	dataLifecycleIndexesV74PreflightIdentity = "data-lifecycle-snapshot-indexes-v74-provenance-v1"
	dataLifecycleIndexesV74RuleSpec          = "synon.workspace.data-lifecycle-snapshot-indexes.v74"
)

// Cleanup checks either hash independently. Separate non-unique indexes support
// that OR lookup while allowing many artifact versions to share each snapshot.
var dataLifecycleIndexesV74Migration = versionedSchemaMigration{
	version: 74,
	name:    "data-lifecycle-snapshot-reference-indexes",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        dataLifecycleIndexesV74CallbackID,
		PreflightIdentity: dataLifecycleIndexesV74PreflightIdentity,
		RuleSpec:          dataLifecycleIndexesV74RuleSpec,
	},
	statements: []string{
		`CREATE INDEX artifact_version_provenance_lineage_snapshot_idx
			ON artifact_version_provenance(lineage_snapshot_hash)`,
		`CREATE INDEX artifact_version_provenance_env_snapshot_idx
			ON artifact_version_provenance(env_snapshot_hash)`,
	},
}

func preflightDataLifecycleIndexesV74(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var count int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('artifact_version_provenance')
		WHERE name IN ('lineage_snapshot_hash','env_snapshot_hash') AND type='TEXT'`).Scan(&count); err != nil {
		return err
	}
	if count != 2 {
		return errors.New("data lifecycle index migration requires canonical provenance snapshot hash columns")
	}
	return nil
}
