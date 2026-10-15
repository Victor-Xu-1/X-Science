package workspace

import (
	"context"
	"errors"
	"strings"
)

const (
	managedEndpointOwnerV78CallbackID        = "managed-endpoint-owner-v78-noop"
	managedEndpointOwnerV78PreflightIdentity = "managed-endpoint-owner-v78-predecessor-v1"
	managedEndpointOwnerV78RuleSpec          = "x-science.workspace.managed-endpoint-owner.v78"
)

// Ownership is prospective evidence, not a migration-time guess about an old
// service. Existing registrations and claims retain their exact bytes/state;
// only newly approved typed registrations can create owner/action records.
var managedEndpointOwnerV78Migration = versionedSchemaMigration{
	version: 78, name: "managed-endpoint-durable-owner",
	identityV2: &schemaMigrationIdentityV2{CallbackID: managedEndpointOwnerV78CallbackID,
		PreflightIdentity: managedEndpointOwnerV78PreflightIdentity, RuleSpec: managedEndpointOwnerV78RuleSpec},
	statements: managedEndpointOwnerSchemaStatements,
}

func preflightManagedEndpointOwnerV78(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var targets int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name IN
		('compute_managed_endpoint_instances','compute_managed_endpoint_actions',
		'compute_managed_endpoint_active_owner_idx','compute_managed_endpoint_pending_action_idx')`).Scan(&targets); err != nil {
		return err
	}
	if targets != 0 {
		return errors.New("managed endpoint owner migration target is not empty")
	}
	columns, _, err := readSQLiteTableShape(ctx, executor, "compute_managed_endpoints")
	if err != nil {
		return errors.New("managed endpoint owner migration requires the canonical registration predecessor")
	}
	required := map[string]string{
		"name": "TEXT", "owner_user_id": "TEXT", "url": "TEXT", "port": "INTEGER",
		"state": "TEXT", "location": "TEXT", "skill_name": "TEXT", "credential_name": "TEXT",
		"live_path": "TEXT", "start_script": "TEXT", "stop_script": "TEXT", "approved_script_hash": "TEXT",
		"last_error": "TEXT", "transcript": "TEXT", "state_changed_at": "TIMESTAMP",
		"service_dir": "TEXT", "service_dir_bytes": "INTEGER", "stop_claim_token": "TEXT", "stop_claim_generation": "INTEGER",
	}
	for _, column := range columns {
		if column.name == "owner_spec_json" {
			return errors.New("managed endpoint owner migration target is not empty")
		}
		if column.name == "name" && column.primaryKey != 1 || column.name != "name" && column.primaryKey != 0 {
			return errors.New("managed endpoint owner migration requires the canonical registration key")
		}
		if want, found := required[column.name]; found {
			if strings.ToUpper(column.declaredType) != want || column.hidden != 0 {
				return errors.New("managed endpoint owner migration requires canonical registration columns")
			}
			delete(required, column.name)
		}
	}
	if len(required) != 0 {
		return errors.New("managed endpoint owner migration requires canonical registration columns")
	}
	return nil
}
