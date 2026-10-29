package workspace

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/kernelcontract/bashsource"
)

func TestKernelWorkerStartupV88JournalOnlyAtomicAndIdempotent(t *testing.T) {
	db, path := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 87); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	ddl := literatureIdentityV83Values(t, db, literatureIdentityV83SchemaQuery)
	if len(journal) != 87 || len(before["projects"]) == 0 || len(before["mutation_result_ledger"]) == 0 {
		t.Fatal("fixture requires the actual v87 prefix and nonempty legacy data")
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_kernel_startup_v88 BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=88 BEGIN SELECT RAISE(ABORT,'injected startup reader floor failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 88); err == nil ||
		!strings.Contains(err.Error(), "injected startup reader floor failure") {
		t.Fatalf("expected the journal transaction to fail, got %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_kernel_startup_v88`); err != nil {
		t.Fatal(err)
	}
	assertData := func() {
		t.Helper()
		if !reflect.DeepEqual(before, transcriptWebMigrationRows(t, db)) ||
			!reflect.DeepEqual(ddl, literatureIdentityV83Values(t, db, literatureIdentityV83SchemaQuery)) {
			t.Fatal("journal-only startup floor changed business rows, raw ACK or DDL")
		}
	}
	assertData()
	if !reflect.DeepEqual(journal, literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)) {
		t.Fatal("failed floor left a partial marker or rewrote the prefix")
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 88); err != nil {
		t.Fatal(err)
	}
	checksum, err := kernelWorkerStartupV88Migration.validatedChecksum()
	if err != nil || len(checksum) != 64 || len(kernelWorkerStartupV88Migration.statements) != 0 {
		t.Fatalf("invalid journal-only identity: %s %v", checksum, err)
	}
	latest := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if len(latest) != 88 || !reflect.DeepEqual(journal, latest[:87]) || len(readMigrationJournal(t, path)) != 88 {
		t.Fatal("floor must append exactly one marker with the original prefix untouched")
	}
	assertData()
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, func() time.Time { return time.Now().Add(time.Hour) }, 88); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(latest, literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)) {
		t.Fatal("floor replay changed its original marker or timestamp")
	}
	assertData()
	t.Logf("startup floor version=88 name=%s checksum=%s", kernelWorkerStartupV88Migration.name, checksum)
}

func TestKernelWorkerStartupV88StoreReopenPreservesOldAndRestrictedSpecs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "fixture", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string]KernelExecutionBackend)
	for _, mode := range []string{"legacy", "restricted"} {
		spec := KernelExecutionSessionSpecV1{Version: 1, KernelID: "kernel-" + mode, OwnerUserID: "owner", ProjectID: "project",
			RootFrameID: frame.RootFrameID, RootFrameIncarnationID: frame.IncarnationID, FrameID: frame.ID,
			FrameIncarnationID: frame.IncarnationID, AgentName: "fixture", KernelKind: "bash", Language: "python", Environment: "system", WorkspaceDir: "/tmp/fixture"}
		if mode == "restricted" {
			spec.WorkerStartupPolicy = bashsource.CanonicalBashStdlibV1
			spec.SupervisorRuntimeGeneration = strings.Repeat("a", 64)
		}
		backend, err := store.CreateKernelExecutionBackend(t.Context(), CreateKernelExecutionBackendInput{
			BackendID: "backend-" + mode, OwnerUserID: "owner", ProjectID: "project", RootFrameID: frame.RootFrameID,
			RootFrameIncarnationID: frame.IncarnationID, FrameID: frame.ID, FrameIncarnationID: frame.IncarnationID,
			KernelID: spec.KernelID, KernelGeneration: 1, SessionSpec: spec, ExecutorInstanceID: "executor-" + mode,
			MachineBootID: "fixture-boot", SocketPath: "/tmp/fixture-" + mode + ".sock", BackendGeneration: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		original[mode] = backend
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for mode, before := range original {
		after, found, err := store.GetKernelExecutionBackend(context.Background(), before.BackendID)
		if err != nil || !found || after.SessionSpecJSON != before.SessionSpecJSON || after.SessionSpecSHA256 != before.SessionSpecSHA256 {
			t.Fatalf("%s reopen changed original canonical metadata: found=%v err=%v", mode, found, err)
		}
		spec, err := DecodeKernelExecutionSessionSpecV1(after.SessionSpecJSON)
		if err != nil || (spec.WorkerStartupPolicy != "") != (mode == "restricted") ||
			(mode == "restricted" && spec.SupervisorRuntimeGeneration != strings.Repeat("a", 64)) {
			t.Fatalf("%s reopen dropped startup identity: %#v err=%v", mode, spec, err)
		}
	}
}
