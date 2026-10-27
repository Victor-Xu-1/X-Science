package workspace

import (
	"context"
	"errors"
)

const (
	memoryExtractionCadenceV82CallbackID        = "memory-extraction-cadence-v82-noop"
	memoryExtractionCadenceV82PreflightIdentity = "memory-extraction-cadence-v82-absent-v1"
	memoryExtractionCadenceV82RuleSpec          = "x-science.workspace.memory-extraction-cadence.v82"
)

var memoryExtractionCadenceV82Migration = versionedSchemaMigration{
	version: 82, name: "durable-memory-completion-cadence",
	identityV2: &schemaMigrationIdentityV2{
		CallbackID: memoryExtractionCadenceV82CallbackID, PreflightIdentity: memoryExtractionCadenceV82PreflightIdentity, RuleSpec: memoryExtractionCadenceV82RuleSpec,
	},
	statements: []string{`ALTER TABLE project_runtime_metadata ADD COLUMN memory_completion_ordinal INTEGER NOT NULL DEFAULT 0
	 CHECK(typeof(memory_completion_ordinal)='integer' AND memory_completion_ordinal>=0)`},
}

func preflightMemoryExtractionCadenceV82(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('project_runtime_metadata') WHERE name='memory_completion_ordinal'`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("Memory cadence migration target is already present")
	}
	return nil
}
