//go:build linux

package detached

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUserExecutorLauncherUsesOneIndependentService(t *testing.T) {
	root := t.TempDir()
	argumentsPath := filepath.Join(root, "arguments")
	runner := filepath.Join(root, "systemd-run")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >" + argumentsPath + "\n"
	if err := os.WriteFile(runner, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "synon-go")
	logPath := filepath.Join(root, "executor.log")
	meminfo := filepath.Join(root, "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal:       12582912 kB\nMemAvailable:   10485760 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &SystemdUserExecutorLauncher{systemdRun: runner, meminfoPath: meminfo}
	err := launcher.Launch(ExecutorLaunchRequest{
		BackendID: "kernel-backend-01234567-89ab-cdef-0123-456789abcdef", BackendGeneration: 3,
		Executable: executable, Arguments: []string{"kernel-executor", "--home", root}, WorkingDirectory: root, LogPath: logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(raw)
	for _, required := range []string{
		"--user\n", "--collect\n", "--service-type=exec\n",
		"--working-directory=" + root + "\n",
		"--unit=synon-kernel-executor-01234567-89ab-cdef-0123-456789abcdef-g3.service\n",
		"--property=KillMode=control-group\n", "--property=MemoryHigh=5583457485\n",
		"--property=Delegate=memory\n", "--property=DelegateSubgroup=control\n",
		"--property=MemoryMax=5583457485\n", "--property=MemorySwapMax=536870912\n",
		"--property=OOMPolicy=continue\n",
		"--property=StandardOutput=append:" + logPath + "\n",
		"--\n" + executable + "\nkernel-executor\n--home\n" + root + "\n",
	} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("systemd launcher arguments missing %q:\n%s", required, arguments)
		}
	}
	if err := launcher.Launch(ExecutorLaunchRequest{BackendID: "unsafe/unit", BackendGeneration: 1, Executable: executable, WorkingDirectory: root, LogPath: logPath}); err == nil {
		t.Fatal("unsafe transient unit identity was accepted")
	}
}

func TestExecutorMemoryLimitsPreserveControlPlane(t *testing.T) {
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal: 8388608 kB\nMemAvailable: 6291456 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	max, err := executorMemoryBudget(meminfo)
	if err != nil {
		t.Fatal(err)
	}
	if max != 3006477108 {
		t.Fatalf("memory budget=%d", max)
	}
	if max >= 8388608*1024 {
		t.Fatalf("unsafe memory budget=%d", max)
	}
}

func TestExecutorMemoryFloorCannotInventUnavailableCapacity(t *testing.T) {
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal: 8388608 kB\nMemAvailable: 524288 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	budget, err := executorMemoryBudget(meminfo)
	var unavailable *ExecutorResourceUnavailableError
	if budget != 0 || !errors.As(err, &unavailable) || unavailable.RequiredBytes != executorMemoryFloorBytes {
		t.Fatalf("unsafe invented budget=%d err=%v", budget, err)
	}
}

func TestSystemdExecutorObservationDistinguishesAbsentUnitFromControlFailure(t *testing.T) {
	root := t.TempDir()
	control := filepath.Join(root, "systemctl")
	launcher := &SystemdUserExecutorLauncher{systemctl: control}
	if err := os.WriteFile(control, []byte("#!/bin/sh\nprintf 'LoadState=not-found\\nActiveState=inactive\\nSubState=dead\\n'; exit 4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := launcher.Observe(context.Background(), "kernel-backend-resource-fixture", 1)
	if err != nil || state != ExecutorLaunchExited {
		t.Fatalf("affirmative missing unit not recognized: %s %v", state, err)
	}
	if err := os.WriteFile(control, []byte("#!/bin/sh\necho 'control unavailable' >&2; exit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err = launcher.Observe(context.Background(), "kernel-backend-resource-fixture", 1)
	if err == nil || state != ExecutorLaunchUnknown {
		t.Fatalf("control failure invented absence: %s %v", state, err)
	}
}

func TestDefaultSharedSocketRootUsesPrivateRuntimeDirectory(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("/tmp", "sx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	root, err := DefaultSharedSocketRoot("/home/example/product")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(root) != runtimeDir || !strings.HasPrefix(filepath.Base(root), "synon-kernel-") {
		t.Fatalf("shared socket root=%q", root)
	}
	if err := os.Chmod(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultSharedSocketRoot("/home/example/product"); err == nil {
		t.Fatal("public runtime directory was accepted")
	}
}
