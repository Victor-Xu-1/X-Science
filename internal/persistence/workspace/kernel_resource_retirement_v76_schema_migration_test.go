package workspace

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func kernelResourceRetirementV75Fixture(t *testing.T) (*Store, KernelExecutionBackend, DetachedKernelExecution, string) {
	t.Helper()
	ctx := context.Background()
	store, backend, history := unstartedBackendV68Fixture(t)
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 75); err != nil {
		t.Fatal(err)
	}
	backend, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
	if err != nil || !found {
		t.Fatalf("legacy backend missing: found=%t err=%v", found, err)
	}
	if _, admitted, err := store.AdmitKernelResources(ctx, backend, 0, KernelResourceCapacity{
		AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: 128 << 20,
	}); err != nil || admitted {
		t.Fatalf("legacy resource wait not established: admitted=%t err=%v", admitted, err)
	}
	if cancelled, err := store.CancelWaitingKernelResources(ctx, backend.BackendID, backend.BackendGeneration); err != nil || !cancelled {
		t.Fatalf("legacy release fence not established: cancelled=%t err=%v", cancelled, err)
	}
	history, found, err = store.GetDetachedKernelExecution(ctx, history.ExecutionID)
	if err != nil || !found {
		t.Fatalf("legacy execution missing: found=%t err=%v", found, err)
	}
	var sequence int
	var name, path string
	if err := store.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil || path == "" {
		t.Fatalf("legacy database path unavailable: %v", err)
	}
	return store, backend, history, path
}

func TestKernelResourceRetirementV76UpgradePreservesDataAndJournal(t *testing.T) {
	ctx := context.Background()
	store, backend, history, path := kernelResourceRetirementV75Fixture(t)
	before, err := store.SchemaStatus(ctx)
	if err != nil || before.CurrentVersion != 75 {
		t.Fatalf("legacy schema=%+v err=%v", before, err)
	}
	reservation, _, err := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil {
		t.Fatal(err)
	}
	finish := FinishKernelExecutionBackendInput{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID}
	if _, err := store.FinishKernelExecutionBackend(ctx, finish); err == nil || !strings.Contains(err.Error(), "kernel startup terminal receipt is required") {
		t.Fatalf("schema 75 did not reproduce the missing retirement transition: %v", err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if upgraded.schemaBackupPath == "" {
		t.Fatal("schema 75 upgrade did not preserve its backup")
	}
	backup, err := sql.Open(sqliteDriver, upgraded.schemaBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	assertSchemaJournalVersion(t, backup, 75)
	if err := preflightKernelResourceRetirementV76(ctx, backup); err != nil {
		t.Fatalf("backup lost the original schema 75 guard: %v", err)
	}
	after, err := upgraded.SchemaStatus(ctx)
	if err != nil || after.CurrentVersion != workspaceSchemaVersion || len(after.Migrations) != workspaceSchemaVersion ||
		after.Migrations[75].Version != 76 || !reflect.DeepEqual(before.Migrations, after.Migrations[:75]) {
		t.Fatalf("upgrade changed historical journal or omitted version 76: %+v err=%v", after, err)
	}
	current, found, err := upgraded.GetKernelExecutionBackend(ctx, backend.BackendID)
	if err != nil || !found || !reflect.DeepEqual(current, backend) {
		t.Fatalf("migration changed old backend data: %+v err=%v", current, err)
	}
	retained, found, err := upgraded.GetDetachedKernelExecution(ctx, history.ExecutionID)
	if err != nil || !found || !reflect.DeepEqual(retained, history) {
		t.Fatalf("migration changed historical execution: %+v err=%v", retained, err)
	}
	resource, found, err := upgraded.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || !reflect.DeepEqual(resource, reservation) {
		t.Fatalf("migration rewrote immutable resource demand/evidence: %+v err=%v", resource, err)
	}
	if err := applyVersionedSchemaMigrations(ctx, upgraded.db, time.Now); err != nil {
		t.Fatal(err)
	}
	repeated, err := upgraded.SchemaStatus(ctx)
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatalf("migration retry changed the journal: %+v err=%v", repeated, err)
	}
	for i := 0; i < 2; i++ {
		terminal, err := upgraded.FinishKernelExecutionBackend(ctx, finish)
		if err != nil || terminal.State != KernelExecutionBackendStateStopped || terminal.StateVersion != backend.StateVersion+1 {
			t.Fatalf("released zero-reservation retirement is not idempotent: %+v err=%v", terminal, err)
		}
	}
	if _, found, err := upgraded.GetKernelStartupFailure(ctx, backend.BackendID, backend.BackendGeneration); err != nil || found {
		t.Fatalf("retirement fabricated a startup failure: found=%t err=%v", found, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.schemaBackupPath != "" {
		t.Fatal("already-upgraded reopen created another migration backup")
	}
	restarted, err := reopened.SchemaStatus(ctx)
	if err != nil || !reflect.DeepEqual(after, restarted) {
		t.Fatalf("reopen changed the schema journal: %+v err=%v", restarted, err)
	}
}

func TestKernelResourceRetirementV76RejectsUnsafeTerminalTransitions(t *testing.T) {
	ctx := context.Background()
	store, backend, _, _ := kernelResourceRetirementV75Fixture(t)
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 76); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, change, terminalExtra string }{
		{"waiting", `UPDATE kernel_resource_reservations SET state='waiting'`, ""},
		{"reserved", `UPDATE kernel_resource_reservations SET state='reserved'`, ""},
		{"previously_admitted", `UPDATE kernel_resource_reservations SET reserved_bytes=1073741824`, ""},
		{"wrong_boot", `UPDATE kernel_resource_reservations SET machine_boot_id='other-boot'`, ""},
		{"wrong_generation", `UPDATE kernel_resource_reservations SET backend_generation=backend_generation+1`, ""},
		{"wrong_demand", `UPDATE kernel_resource_reservations SET requested_bytes=1073741824`, ""},
		{"no_ledger", `DELETE FROM kernel_resource_reservations`, ""},
		{"executor", `UPDATE kernel_execution_backends SET state_version=state_version+1,executor_pid=7,executor_pid_start_ticks=9`, ""},
		{"worker", `UPDATE kernel_execution_backends SET state_version=state_version+1,worker_pid=7,worker_pid_start_ticks=9`, ""},
		{"process_group", `UPDATE kernel_execution_backends SET state_version=state_version+1,worker_pgid=7`, ""},
		{"cgroup", `UPDATE kernel_execution_backends SET state_version=state_version+1,cgroup_path='/owned'`, ""},
		{"heartbeat_sequence", `UPDATE kernel_execution_backends SET state_version=state_version+1,heartbeat_sequence=1`, ""},
		{"heartbeat_time", `UPDATE kernel_execution_backends SET state_version=state_version+1,heartbeat_at='2026-01-01T00:00:00Z'`, ""},
		{"new_physical_claim", "", `,executor_pid=7,executor_pid_start_ticks=9`},
		{"new_executor_identity", "", `,executor_instance_id='other-executor'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if tc.change != "" {
				if _, err := tx.ExecContext(ctx, tc.change); err != nil {
					t.Fatalf("unsafe-case fixture rejected before the terminal guard: %v", err)
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE kernel_execution_backends SET state='stopped',state_version=state_version+1`+tc.terminalExtra+` WHERE backend_id=?`, backend.BackendID); err == nil || !strings.Contains(err.Error(), "kernel startup terminal receipt is required") {
				t.Fatalf("unsafe terminal transition was not rejected by the retained receipt guard: %v", err)
			}
		})
	}
	// Existing genuine startup-failure evidence remains sufficient; the new
	// ledger route must not remove the historical receipt authority.
	if _, err := store.db.Exec(`DELETE FROM kernel_resource_reservations`); err != nil {
		t.Fatal(err)
	}
	if err := store.FailKernelExecutorStartup(ctx, KernelStartupFailure{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, Stage: "worker_start"}); err != nil {
		t.Fatalf("existing startup failure contract regressed: %v", err)
	}
}

func TestKernelResourceRetirementV76FailureRestoresGuardAndJournal(t *testing.T) {
	ctx := context.Background()
	store, backend, _, _ := kernelResourceRetirementV75Fixture(t)
	before, err := store.SchemaStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_v76_journal BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=76 BEGIN SELECT RAISE(ABORT,'injected-v76'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 76); err == nil || !strings.Contains(err.Error(), "injected-v76") {
		t.Fatalf("injected migration failure did not occur: %v", err)
	}
	after, err := store.SchemaStatus(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed migration changed its journal: %+v err=%v", after, err)
	}
	if err := preflightKernelResourceRetirementV76(ctx, store.db); err != nil {
		t.Fatalf("failed migration did not restore the original guard: %v", err)
	}
	current, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
	if err != nil || !found || !reflect.DeepEqual(current, backend) {
		t.Fatalf("failed migration changed the backend: %+v err=%v", current, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_v76_journal`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, store.db, time.Now, 76); err != nil {
		t.Fatal(err)
	}
	var violations int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("migration broke the reference graph: violations=%d err=%v", violations, err)
	}
}
