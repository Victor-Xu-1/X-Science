//go:build linux && amd64

package kernel

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"x-science/internal/kernelcontract/bashsource"
)

// Opt in with a verified installed bundle and a fresh, delegated test unit. The
// bundle is read-only input: this case installs nothing and launches no task.
func TestBashStartupBundledEgressSurvivesRealConfinement(t *testing.T) {
	conda := os.Getenv("X_SCIENCE_TEST_BOOTSTRAP_CONDA")
	unit := os.Getenv("X_SCIENCE_TEST_BOOTSTRAP_UNIT")
	if conda == "" || !strings.HasPrefix(unit, "x-science-bootstrap-fixture-") || !strings.HasSuffix(unit, ".service") {
		t.Skip("opt-in owned delegated-unit bundled-runtime bootstrap")
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	relative := strings.TrimPrefix(strings.TrimSpace(string(cgroup)), "0::")
	if !filepath.IsAbs(relative) || filepath.Base(relative) != "control" || filepath.Base(filepath.Dir(relative)) != unit {
		t.Fatal("fixture is outside its explicitly owned delegated unit")
	}
	domain := filepath.Join("/sys/fs/cgroup", filepath.Dir(relative), "bootstrap-worker")
	if err := os.Mkdir(domain, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(domain); err != nil {
			t.Errorf("owned worker cgroup did not settle: %v", err)
		}
	})
	assets := filepath.Join(repositoryRootForCondaRuntimeTest(t), "assets", "optional")
	t.Setenv("X_SCIENCE_KERNEL_ASSET_ROOT", assets)
	t.Setenv("X_SCIENCE_KERNEL_PYTHON", "")
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	manager, err := DiscoverManagerWithPaths(conda, filepath.Join(conda, "envs"))
	if err != nil {
		t.Fatal(err)
	}
	policy, generation := manager.CanonicalBashStartup()
	if policy != bashsource.CanonicalBashStdlibV1 || generation == "" {
		t.Fatal("installed bundle did not provide the real canonical startup capability")
	}
	spec := startupSessionFixture(generation, t.TempDir())
	python, _, environment, mounts, err := manager.sessionRuntimeWithMounts(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFrozenWorkerMounts(mounts)
	if kernelPythonRuntimePrefix(python) == "" {
		t.Fatal("fixture requires a real non-system standard-library distribution")
	}
	proxy, err := startKernelEgressProxy(spec.WorkspaceDir, "owned-bootstrap-fixture", []string{"example.invalid"}, nil)
	if err != nil || proxy == nil {
		t.Fatalf("closed egress authority: %v", err)
	}
	defer proxy.Close()
	forwarder := filepath.Join(assets, "kernels")
	if !kernelMountsCoverPath(mounts, forwarder) {
		mounts = append(mounts, TrustedReadOnlyDirectoryMount(forwarder))
	}
	program, arguments, environment, err := manager.wrapKernelEgressRuntime(python,
		[]string{"-I", "-S", "-c", "print('bootstrap-ready')"}, environment, proxy.port, "", policy)
	if err != nil {
		t.Fatal(err)
	}
	// Diagnostic-only command copy: retain the original algorithm, 100 attempts,
	// isolated interpreter, mounts and policy, while exposing bootstrap stderr.
	arguments[4] = strings.ReplaceAll(arguments[4], ">/dev/null 2>&1", ">&2")
	arguments[4] = strings.ReplaceAll(arguments[4], " 2>/dev/null", "")
	child := proxy.takeChild()
	if child == nil {
		t.Fatal("missing original closed-egress descriptor")
	}
	defer child.Close()
	environment = mergeKernelEnvironment(environment, map[string]string{
		"X_SCIENCE_KERNEL_EGRESS_CONTROL_FD": strconv.Itoa(4 + len(mounts)),
	})
	command, err := newConfinedWorkerCommandWithAuxiliary(spec.WorkspaceDir, program, arguments, environment, mounts, nil, []*os.File{child})
	if err != nil {
		t.Fatal(err)
	}
	defer closeKernelCommandExtraFiles(command)
	var output, diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostics
	process, err := startWorkerProcess(command, domain)
	if err != nil {
		t.Fatal(err)
	}
	defer process.close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = process.kill() // Only the original owner and captured start identity.
		<-done
		t.Fatalf("owned bootstrap deadline: %s\n%s", output.String(), diagnostics.String())
	}
	if err != nil || strings.TrimSpace(output.String()) != "bootstrap-ready" {
		t.Fatalf("confined bundled bootstrap failed: %v\n%s\n%s", err, output.String(), diagnostics.String())
	}
	t.Log("REAL_CONFINED_BUNDLED_EGRESS_READY_WITH_ORIGINAL_RESOURCE_AND_POLICY")
}
