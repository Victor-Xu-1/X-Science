package workspace

import (
	"context"
	"errors"
	"strings"
)

const (
	kernelResourceRetirementV76CallbackID        = "kernel-resource-retirement-v76-noop"
	kernelResourceRetirementV76PreflightIdentity = "kernel-resource-retirement-v76-startup-ledger-v1"
	kernelResourceRetirementV76RuleSpec          = "x-science.workspace.kernel-resource-retirement.v76"
	kernelStartupTerminalReceiptV69              = `CREATE TRIGGER kernel_startup_terminal_receipt BEFORE UPDATE ON kernel_execution_backends
		WHEN OLD.state='starting' AND NEW.state='stopped' AND NOT EXISTS(
		SELECT 1 FROM kernel_execution_startup_receipts r WHERE r.backend_id=NEW.backend_id
		AND r.backend_generation=NEW.backend_generation AND r.executor_instance_id=NEW.executor_instance_id)
		BEGIN SELECT RAISE(ABORT,'kernel startup terminal receipt is required'); END`
)

// A cancelled, never-admitted resource reservation is a non-start terminal
// witness, not a failed process launch. Keep the historical failure receipt
// rule for every other starting generation, and never invent a process ID.
var kernelResourceRetirementV76Migration = versionedSchemaMigration{
	version: 76, name: "kernel-unlaunched-resource-retirement",
	identityV2: &schemaMigrationIdentityV2{CallbackID: kernelResourceRetirementV76CallbackID,
		PreflightIdentity: kernelResourceRetirementV76PreflightIdentity, RuleSpec: kernelResourceRetirementV76RuleSpec},
	statements: []string{
		`DROP TRIGGER kernel_startup_terminal_receipt`,
		`CREATE TRIGGER kernel_startup_terminal_receipt BEFORE UPDATE ON kernel_execution_backends
		WHEN OLD.state='starting' AND NEW.state='stopped'
		AND NOT EXISTS(SELECT 1 FROM kernel_execution_startup_receipts r WHERE r.backend_id=NEW.backend_id
			AND r.backend_generation=NEW.backend_generation AND r.executor_instance_id=NEW.executor_instance_id)
		AND NOT EXISTS(SELECT 1 FROM kernel_resource_reservations r WHERE
			NEW.backend_id=OLD.backend_id AND NEW.backend_generation=OLD.backend_generation
			AND NEW.executor_instance_id=OLD.executor_instance_id AND NEW.machine_boot_id=OLD.machine_boot_id
			AND NEW.kernel_id=OLD.kernel_id AND NEW.kernel_generation=OLD.kernel_generation
			AND NEW.session_spec_sha256=OLD.session_spec_sha256 AND NEW.session_spec_json=OLD.session_spec_json
			AND OLD.executor_pid IS NULL AND OLD.executor_pid_start_ticks IS NULL
			AND OLD.worker_pid IS NULL AND OLD.worker_pid_start_ticks IS NULL AND OLD.worker_pgid IS NULL
			AND OLD.cgroup_path='' AND OLD.heartbeat_sequence=0 AND OLD.heartbeat_at IS NULL
			AND NEW.executor_pid IS NULL AND NEW.executor_pid_start_ticks IS NULL
			AND NEW.worker_pid IS NULL AND NEW.worker_pid_start_ticks IS NULL AND NEW.worker_pgid IS NULL
			AND NEW.cgroup_path='' AND NEW.heartbeat_sequence=0 AND NEW.heartbeat_at IS NULL
			AND r.backend_id=OLD.backend_id
				AND r.backend_generation=OLD.backend_generation AND r.machine_boot_id=OLD.machine_boot_id
				AND r.state='released' AND r.reserved_bytes=0
				AND r.requested_bytes=COALESCE(json_extract(OLD.session_spec_json,'$.resource_memory_bytes'),0))
		BEGIN SELECT RAISE(ABORT,'kernel startup terminal receipt is required'); END`,
	},
}

func preflightKernelResourceRetirementV76(ctx context.Context, executor schemaMigrationQueryExecutor) error {
	var prior string
	if err := executor.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='kernel_startup_terminal_receipt' AND tbl_name='kernel_execution_backends'`).Scan(&prior); err != nil {
		return err
	}
	if strings.Join(strings.Fields(prior), " ") != strings.Join(strings.Fields(kernelStartupTerminalReceiptV69), " ") {
		return errors.New("resource retirement migration requires the canonical startup terminal receipt guard")
	}
	var columns int
	if err := executor.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('kernel_resource_reservations') WHERE name IN ('backend_id','backend_generation','machine_boot_id','requested_bytes','reserved_bytes','state')`).Scan(&columns); err != nil {
		return err
	}
	if columns != 6 {
		return errors.New("resource retirement migration requires the canonical resource ledger")
	}
	return nil
}
