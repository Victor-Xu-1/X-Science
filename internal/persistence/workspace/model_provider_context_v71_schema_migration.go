package workspace

import (
	"context"
	"errors"
)

const (
	modelProviderContextV71CallbackID        = "model-provider-context-window-v71-noop"
	modelProviderContextV71PreflightIdentity = "model-provider-context-window-v71-upgrade-v1"
	modelProviderContextV71RuleSpec          = "synon.workspace.model-provider.context-window.v71"
)

// NULL is deliberately unknown. Output limits and alias names are not evidence
// of a model's context capacity, so existing profiles are never backfilled.
var modelProviderContextV71Migration = versionedSchemaMigration{
	version: 71,
	name:    "model-provider-context-window",
	statements: []string{`ALTER TABLE model_providers ADD COLUMN context_window INTEGER
		CHECK (context_window IS NULL OR (typeof(context_window) = 'integer' AND context_window >= 1 AND context_window <= 10000000))`},
	identityV2: &schemaMigrationIdentityV2{
		CallbackID:        modelProviderContextV71CallbackID,
		PreflightIdentity: modelProviderContextV71PreflightIdentity,
		RuleSpec:          modelProviderContextV71RuleSpec,
	},
}

func preflightModelProviderContextV71(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var count int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('model_providers') WHERE name IN ('id','user_id','type','base_url','model','temperature','max_tokens')`).Scan(&count); err != nil {
		return err
	}
	if count != 7 {
		return errors.New("canonical model provider generation schema is required")
	}
	return nil
}
