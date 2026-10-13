package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentKernelChecksDirectoriesBeforeRuntimePreparation(t *testing.T) {
	store, _, _ := newTranscriptWebFixture(t)
	createKernelAPIProjectAndFrame(t, store, "project-root-admission", "owner-root-admission", "frame-root-admission", "OPERON", "")
	access, found, err := store.GetKernelFrameAccess("frame-root-admission")
	if err != nil || !found {
		t.Fatalf("frame access found=%t err=%v", found, err)
	}
	for _, tool := range []string{"python", "bash", "r", "repl"} {
		t.Run(tool, func(t *testing.T) {
			missing := filepath.Join(t.TempDir(), "external-runtime")
			app := &Server{workspaceStore: store, fileRoot: t.TempDir(), condaHome: missing, condaEnvsPath: filepath.Join(missing, "envs")}
			input := map[string]any{"code": "print(1)", "environment": agentKernelManagedPythonEnvironment, "human_description": "Check directory admission"}
			if tool == "bash" {
				delete(input, "code")
				input["command"] = "printf 1"
			} else if tool == "r" {
				input["environment"] = "r"
			} else if tool == "repl" {
				delete(input, "environment")
			}
			// There deliberately is no runtime manager: the directory guard must
			// run before any manager/provisioning path can be reached.
			result, err := app.executeAgentKernelTool(context.Background(), &agentKernelContext{access: access}, tool, input)
			if err != nil || result["code"] != "kernel_runtime_directories_unavailable" || result["executed"] != false {
				t.Fatalf("directory admission did not precede preparation: result=%#v err=%v", result, err)
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("execution provisioned an external root: %v", err)
			}
		})
	}
}

func TestAgentKernelManagedDirectoriesAcceptConfiguredExternalRoots(t *testing.T) {
	root, conda := t.TempDir(), t.TempDir()
	envs := filepath.Join(conda, "envs")
	if err := os.Mkdir(envs, 0o700); err != nil {
		t.Fatal(err)
	}
	app := &Server{fileRoot: root, condaHome: conda, condaEnvsPath: envs}
	if err := app.ensureAgentKernelManagedDirectories(); err != nil {
		t.Fatalf("existing operator-configured runtime outside task data must be usable: %v", err)
	}
	protected, err := app.agentKernelProtectedPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, conda, envs} {
		if !stringSliceContains(protected, path) {
			t.Fatalf("runtime lost protection: %s", path)
		}
	}
	if _, err := app.agentKernelConfinementMounts("owner", envs, protected); err == nil {
		t.Fatal("configured runtime must not become a writable task workspace")
	}
}

func TestAgentKernelManagedDirectoriesNeverCreateMissingExternalRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	app := &Server{fileRoot: t.TempDir(), condaHome: missing}
	if err := app.ensureAgentKernelManagedDirectories(); err == nil {
		t.Fatal("missing external runtime was admitted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("external runtime was created: %v", err)
	}
}

func TestAgentKernelReadinessIsReadOnlyAndConfigurationFailureIsNonExecuting(t *testing.T) {
	root := t.TempDir()
	app := &Server{fileRoot: root, condaHome: filepath.Join(root, "not-installed")}
	if app.agentKernelManagedDirectoriesReady() {
		t.Fatal("missing installed directory reported ready")
	}
	if _, err := os.Stat(app.condaHome); !os.IsNotExist(err) {
		t.Fatalf("readiness mutated directories: %v", err)
	}
	result := agentKernelRuntimeConfigurationPreflight()
	if !agentKernelPreflightResult(result) || result["retryable"] != false || result["executed"] != false {
		t.Fatalf("configuration failure lost non-execution proof: %#v", result)
	}
	if err := app.ensureAgentKernelManagedDirectories(); err != nil {
		t.Fatal(err)
	}
	if !app.agentKernelManagedDirectoriesReady() {
		t.Fatal("repaired configuration did not become ready")
	}
}
