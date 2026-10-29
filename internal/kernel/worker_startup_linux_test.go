//go:build linux

package kernel

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/kernelcontract/bashsource"
)

// Uses the original activation marker/helper checks on a test-owned venv.
// It installs no package, touches no user runtime, and claims no scientific proof.
func startupManagerFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	assets := filepath.Join(repositoryRootForCondaRuntimeTest(t), "assets", "optional")
	t.Setenv("X_SCIENCE_KERNEL_ASSET_ROOT", assets)
	t.Setenv("X_SCIENCE_KERNEL_PYTHON", "")
	manager, err := DiscoverManagerWithPaths(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if policy, generation := manager.CanonicalBashStartup(); policy != "" || generation != "" {
		t.Fatal("missing installation granted capability")
	}
	installed, err := loadManagedPythonRuntime(manager.config)
	if err != nil {
		t.Fatal(err)
	}
	prefix := manager.managedPythonGenerationPath(installed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-S", "-m", "venv", "--without-pip", "--copies", prefix)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture venv: %v %s", err, output)
	}
	if err := manager.installManagedPythonHelpers(installed, prefix); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedRuntimeMarker(filepath.Join(prefix, managedRuntimeMarkerName), manager.managedPythonMarker(installed)); err != nil {
		t.Fatal(err)
	}
	if err := manager.activateManagedPythonGeneration(installed); err != nil {
		t.Fatal(err)
	}
	executable, err := manager.ManagedPythonExecutable()
	if err != nil {
		t.Fatal(err)
	}
	return manager, executable, installed.activationGeneration
}

func startupSessionFixture(generation, workspace string) SessionSpec {
	return SessionSpec{OwnerID: "o", ProjectID: "p", FrameID: "f", FrameIncarnationID: "fi", RootFrameID: "r",
		RootFrameIncarnationID: "ri", AgentName: "a", KernelKind: "bash", Language: "python", Environment: "system",
		WorkspaceDir: workspace, WorkerStartupPolicy: bashsource.CanonicalBashStdlibV1, SupervisorRuntimeGeneration: generation}
}

func TestBashStartupRequiresActualDefaultInstalledGeneration(t *testing.T) {
	manager, executable, generation := startupManagerFixture(t)
	policy, current := manager.CanonicalBashStartup()
	if policy != bashsource.CanonicalBashStdlibV1 || current != generation {
		t.Fatal("installed authority not derived")
	}
	spec := startupSessionFixture(generation, t.TempDir())
	selected, args, _, err := manager.sessionRuntime(spec)
	if err != nil || selected != executable || len(args) < 3 || args[0] != "-I" || args[1] != "-S" {
		t.Fatalf("startup %s %v %v", selected, args, err)
	}
	spec.SupervisorRuntimeGeneration = strings.Repeat("b", 64)
	if _, err := manager.sessionSupervisor(spec); err == nil {
		t.Fatal("generation mismatch silently downgraded")
	}
	config := manager.config
	config.defaultPythonDiscovery = false
	if policy, _ := NewManager(config).CanonicalBashStartup(); policy != "" {
		t.Fatal("explicit interpreter acquired capability")
	}
	spec.SupervisorRuntimeGeneration = generation
	if _, err := NewManager(config).sessionSupervisor(spec); err == nil {
		t.Fatal("caller-supplied policy bypassed discovery authority")
	}
	restored, err := DiscoverManagerWithPaths(manager.config.CondaHome, manager.config.CondaEnvsPath)
	if err != nil {
		t.Fatal(err)
	}
	if selected, err := restored.sessionSupervisor(spec); err != nil || selected != executable {
		t.Fatalf("manager reconstruction did not verify the original installed generation: %s %v", selected, err)
	}
	changed := spec
	changed.SupervisorRuntimeGeneration = strings.Repeat("b", 64)
	if _, err := restored.sessionSupervisor(changed); err == nil {
		t.Fatal("reconstructed manager accepted a caller-declared generation")
	}
	t.Setenv("X_SCIENCE_KERNEL_PYTHON", executable)
	explicit, err := DiscoverManagerWithPaths(manager.config.CondaHome, manager.config.CondaEnvsPath)
	if err != nil {
		t.Fatal(err)
	}
	if policy, _ := explicit.CanonicalBashStartup(); policy != "" {
		t.Fatal("explicit operator override acquired discovery trust")
	}
	manager.config.Environment = map[string]string{"PYTHONPATH": t.TempDir()}
	if policy, _ := manager.CanonicalBashStartup(); policy != "" {
		t.Fatal("custom startup environment acquired capability")
	}
}

func TestBashStartupWorkerRejectsUnknownOrUnisolatedProfile(t *testing.T) {
	manager, executable, _ := startupManagerFixture(t)
	for _, args := range [][]string{
		{manager.config.WorkerPath, "--x-science-startup-policy=" + string(bashsource.CanonicalBashStdlibV1)},
		{"-I", "-S", manager.config.WorkerPath, "--x-science-startup-policy=unknown"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// Invalid isolated starts import the sidecar before refusing the policy.
		command := exec.CommandContext(ctx, executable, append([]string{"-B"}, args...)...)
		command.Env = kernelEnvironment(map[string]string{"PYTHONDONTWRITEBYTECODE": "1"})
		output, err := command.CombinedOutput()
		cancel()
		if err == nil || (!strings.Contains(string(output), "isolated no-site") && !strings.Contains(string(output), "unknown supervisor startup policy")) {
			t.Fatalf("invalid startup was not refused: %v %s", err, output)
		}
	}
}
