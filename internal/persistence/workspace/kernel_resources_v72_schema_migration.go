package workspace

import (
	"context"
	"errors"
)

const (
	kernelResourcesV72CallbackID        = "kernel-resource-reservations-v72-noop"
	kernelResourcesV72PreflightIdentity = "kernel-resource-reservations-v72-backend-v1"
	kernelResourcesV72RuleSpec          = "synon.workspace.kernel-resources.v72"
)

var kernelResourcesV72Migration = versionedSchemaMigration{
	version: 72, name: "kernel-resource-reservations",
	identityV2: &schemaMigrationIdentityV2{CallbackID: kernelResourcesV72CallbackID, PreflightIdentity: kernelResourcesV72PreflightIdentity, RuleSpec: kernelResourcesV72RuleSpec},
	statements: []string{
		`CREATE TABLE kernel_resource_reservations (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			backend_id TEXT NOT NULL REFERENCES kernel_execution_backends(backend_id) ON DELETE CASCADE,
			backend_generation INTEGER NOT NULL CHECK(backend_generation>0),
			machine_boot_id TEXT NOT NULL CHECK(length(machine_boot_id)>0),
			requested_bytes INTEGER NOT NULL CHECK(requested_bytes>=0),
			reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes>=0),
			state TEXT NOT NULL CHECK(state IN ('waiting','reserved','released')),
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(backend_id,backend_generation))`,
		`CREATE INDEX kernel_resource_admission_queue ON kernel_resource_reservations(machine_boot_id,state,sequence)`,
	},
}

func preflightKernelResourcesV72(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var count int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('kernel_execution_backends') WHERE name IN ('backend_id','backend_generation','machine_boot_id','state')`).Scan(&count); err != nil {
		return err
	}
	if count != 4 {
		return errors.New("kernel resource migration requires canonical backend ownership")
	}
	return nil
}
