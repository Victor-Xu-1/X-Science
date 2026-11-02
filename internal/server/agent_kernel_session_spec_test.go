package server

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sciencecapability"
)

func newKernelSessionSpecPreparationFixture(t *testing.T) (*Server, workspace.KernelFrameAccess, string) {
	t.Helper()
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	t.Cleanup(func() { closeKernelHostTestRuntime(t, app, manager, store) })
	return app, identity.access, identity.workspaceDir
}

func TestAgentKernelExecutionSessionSpecRetainsAuthorityAndResources(t *testing.T) {
	srv, access, directory := newKernelSessionSpecPreparationFixture(t)
	runtime := agentKernelSessionRuntime{
		PublicName: "python", Kind: "analysis", Language: "python",
		Environment: "managed-analysis", RuntimeGeneration: "generation-17",
	}
	want, err := srv.agentKernelSessionSpec(t.Context(), access, directory, runtime)
	if err != nil {
		_, authorityErr := canonicalOrCreateAgentWorkspaceDirectory(directory, 0o700)
		t.Fatalf("original session builder: %v; fixture workspace admission: %v", err, authorityErr)
	}
	for _, owned := range []bool{false, true} {
		input := agentKernelExecutionSessionSpecInput{Runtime: runtime, Input: map[string]any{"memory_budget_mb": 1536}, TaskOwned: owned,
			Operation: &workspace.KernelLocalOperation{OperationID: "spec-operation"}}
		got, err := srv.prepareAgentKernelExecutionSessionSpec(t.Context(), access, directory, input)
		expected := want
		expected.ResourceMemoryBytes = 1536 << 20
		if owned {
			expected.KernelID = softwareRuntimeKernelID(input.Operation.OperationID)
		}
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("task-owned=%t session authority changed: got=%+v want=%+v err=%v", owned, got, expected, err)
		}
	}
}

func TestAgentKernelExecutionSessionSpecPreservesFailureOrderAndSpan(t *testing.T) {
	srv, access, directory := newKernelSessionSpecPreparationFixture(t)
	for _, stage := range []string{"spec", "operation", "budget"} {
		t.Run(stage, func(t *testing.T) {
			input := agentKernelExecutionSessionSpecInput{
				Runtime: agentKernelSessionRuntime{PublicName: "python", Kind: "analysis", Language: "python", Environment: "python"},
				Input:   map[string]any{"memory_budget_mb": "invalid"}, TaskOwned: true,
			}
			originalRoot := srv.fileRoot
			defer func() { srv.fileRoot = originalRoot }()
			wantError := "task-owned runtime durable operation authority is unavailable"
			if stage == "spec" {
				srv.fileRoot = filepath.Join(t.TempDir(), "missing-protected-root")
				wantError = "kernel protected paths could not be verified"
			} else if stage == "budget" {
				input.Operation = &workspace.KernelLocalOperation{OperationID: "spec-operation"}
				wantError = "memory_budget_mb must be an integer"
			}
			var record kernelPrepareDiagnostic
			now := time.Now()
			timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{OperationID: "original-operation", Attempt: 3},
				func() time.Time { now = now.Add(time.Microsecond); return now }, func(value kernelPrepareDiagnostic) { record = value })
			ctx := context.WithValue(t.Context(), kernelPrepareTimingContextKey{}, timing)
			_, err := srv.prepareAgentKernelExecutionSessionSpec(ctx, access, directory, input)
			timing.finishReturn(err)
			if err == nil || err.Error() != wantError {
				t.Fatalf("failure order changed: %v want %s", err, wantError)
			}
			if record.OperationID != "original-operation" || record.Attempt != 3 || len(record.Spans) != 1 ||
				record.Spans[0].Stage != "session_spec" || record.Spans[0].US == nil || *record.Spans[0].US != 1 || record.Spans[0].Unknown {
				t.Fatalf("original session-spec span was not closed exactly once: %+v", record)
			}
		})
	}
}

func TestAgentKernelExecutionSessionSpecExcludesOnlyCanonicalPack(t *testing.T) {
	srv, access, directory := newKernelSessionSpecPreparationFixture(t)
	pack := sciencecapability.ExecutionPack{ID: "capability.engine", Mode: "local", Skill: "managed-workflow", Executable: "python", Script: "executionpacks/run.py"}
	srv.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{
		ID: "capability", AcceptedEngines: []sciencecapability.EngineDefinition{{ID: "engine", ExecutionPack: pack}},
	}}}
	runtime := agentKernelSessionRuntime{PublicName: "bash", Kind: "bash", Language: "python", Environment: "python"}
	spec, err := srv.agentKernelSessionSpec(t.Context(), access, directory, runtime)
	if err != nil {
		t.Fatal(err)
	}
	kernelID, err := kernelruntime.StableSessionID(spec)
	if err != nil {
		t.Fatal(err)
	}
	command := `python "` + filepath.Join(directory, ".x-science", "runtime", "skills", "managed-workflow-abcd", "scripts", "run.py") + `"`
	if _, found := srv.canonicalManagedExecutionPack("bash", map[string]any{"command": command}); !found {
		t.Fatal("fixture is not a canonical managed execution")
	}
	// An invalid persisted receipt makes an accidental authority exclusion
	// observable without executing a worker or publishing any output.
	if _, err := srv.workspaceStore.SaveExecutionLog(workspace.SaveExecutionLogInput{
		Record: workspace.ExecutionLogRecord{ID: "spec-receipt", FrameID: access.Frame.ID, KernelID: kernelID,
			KernelKind: "bash", CondaEnv: "python", Language: "python", Source: command, ExitStatus: "ok", Origin: "test",
			FilesWritten: []kernelruntime.FileWrite{{Path: "results/report.md", SHA256: "invalid"}}},
		ExpectedOwnerID: access.UserID, ExpectedProjectID: access.Frame.ProjectID,
		ExpectedFrameIncarnationID: access.Frame.IncarnationID, ExpectedRootFrameIncarnationID: access.RootFrameIncarnationID,
	}); err != nil {
		t.Fatal(err)
	}
	for _, canonical := range []bool{true, false} {
		input := map[string]any{"command": command}
		if !canonical {
			input["command"] = command + " > results/report.md"
		}
		// Caller-provided exclusions must not grant authority to decorated code.
		runtime.ExcludePackID = pack.ID
		_, err := srv.prepareAgentKernelExecutionSessionSpec(t.Context(), access, directory, agentKernelExecutionSessionSpecInput{Runtime: runtime, Input: input})
		if canonical && err != nil || !canonical && (err == nil || !strings.Contains(err.Error(), "invalid file digest")) {
			t.Fatalf("canonical=%t pack exclusion changed: %v", canonical, err)
		}
	}
}
