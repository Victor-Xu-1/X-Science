//go:build linux

package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

const managedEndpointPhysicalImage = "sha256:e06cc1111ed84189e91866447f562b89faadbfbbb9937cd67e6bf4172cdb45df"

// Opt-in physical witnesses are deliberately separate from controlled contract
// tests. The caller must supply the exact built CLI and its content hash; these
// tests neither build a binary nor install an interpreter/image/dependency.
type managedEndpointPhysicalFixture struct {
	managedEndpointOwnerFixture
	ctx         context.Context
	cancel      context.CancelFunc
	binary      string
	wrapper     string
	systemctl   string
	docker      string
	daemon      string
	containerID string
	stores      []*Store
}

func managedEndpointPhysicalPrerequisites(t *testing.T) (string, string) {
	t.Helper()
	binary := os.Getenv("X_SCIENCE_TEST_MANAGED_ENDPOINT_BINARY")
	if binary == "" {
		t.Skip("physical managed-endpoint witness not run: X_SCIENCE_TEST_MANAGED_ENDPOINT_BINARY was not supplied")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("physical witness binary must be an absolute path")
	}
	expected := strings.ToLower(os.Getenv("X_SCIENCE_TEST_MANAGED_ENDPOINT_BINARY_SHA256"))
	if len(expected) != 64 {
		t.Fatal("explicit physical witness requires the exact CLI SHA256")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatal("physical witness CLI is not a regular executable")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expected {
		t.Fatalf("physical witness binary hash differs: got %s", actual)
	}
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		t.Skip("physical managed-endpoint witness not run: systemctl is unavailable")
	}
	for _, name := range []string{"systemd-run", "sleep"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("physical managed-endpoint witness not run: %s is unavailable", name)
		}
	}
	if !filepath.IsAbs(os.Getenv("XDG_RUNTIME_DIR")) {
		t.Skip("physical managed-endpoint witness not run: no absolute XDG_RUNTIME_DIR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state, err := managedEndpointPhysicalCommand(ctx, systemctl, "--user", "is-system-running")
	if state != "running" && state != "degraded" {
		t.Skipf("physical managed-endpoint witness not run: user systemd unavailable (%q, %v)", state, err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("physical managed-endpoint witness not run: existing host python3 is unavailable")
	}
	if _, err := managedEndpointPhysicalCommand(ctx, python, "-c", "import os, signal; assert hasattr(signal, 'pidfd_send_signal'); os.close(os.pidfd_open(os.getpid()))"); err != nil {
		t.Skipf("physical managed-endpoint witness not run: safe same-unit pidfd expiry is unavailable (%v)", err)
	}
	return binary, systemctl
}

func newManagedEndpointPhysicalFixture(t *testing.T, binary, systemctl string, registration compute.ManagedEndpointRegistration) *managedEndpointPhysicalFixture {
	t.Helper()
	fixture := &managedEndpointPhysicalFixture{managedEndpointOwnerFixture: newManagedEndpointOwnerFixture(t, registration), binary: binary, systemctl: systemctl}
	// ClaimManagedEndpointStart has already allocated the 16-byte instance ID
	// inside its Store transaction; the fixture's subsequent Prepare is an
	// idempotent read, not the time-derived fallback ID passed by that helper.
	// The identifier is not cleanup authority: physical identity is checked too.
	if id, err := hex.DecodeString(fixture.instance.ID); err != nil || len(id) != 16 {
		t.Fatalf("physical fixture did not receive its Store-allocated instance: %q", fixture.instance.ID)
	}
	// Go's testing.TempDir creates its numbered child with mode0777 before
	// umask, unlike the product's fresh0700 runtime directories. These two
	// directories were just created exclusively by this fixture: set up that
	// same private contract rather than weakening the production path check.
	for _, directory := range []string{fixture.instance.RuntimeRoot, fixture.instance.ServiceDir} {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("physical fixture directory is not private: %s (%v)", directory, err)
		}
	}
	fixture.stores = []*Store{fixture.store}
	fixture.ctx, fixture.cancel = context.WithTimeout(context.Background(), 45*time.Second)
	fixture.wrapper = filepath.Join(fixture.instance.ServiceDir, "private-owner-worker")
	unit := compute.ManagedEndpointUnitName(fixture.instance.ID)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	timer := filepath.Join(fixture.instance.ServiceDir, "private-owner-expiry.py")
	if err := os.WriteFile(timer, []byte(managedEndpointPhysicalExpiry), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exec keeps the CLI as unit MainPID. The SAME-cgroup expiry child pins its
	// actual parent with pidfd before exec, so a lost systemd control socket or
	// PID reuse cannot turn the timer into a kill of an unrelated process.
	wrapper := "#!/bin/sh\nset -eu\nunit=" + managedEndpointPhysicalQuote(unit) + "\n" +
		"case \"$(cat /proc/self/cgroup)\" in */\"$unit\"*) ;; *) exit 91 ;; esac\n" +
		"invocation=$(" + managedEndpointPhysicalQuote(systemctl) + " --user show --value --property=InvocationID -- \"$unit\")\n" +
		"test -n \"$invocation\"\n" +
		managedEndpointPhysicalQuote(systemctl) + " --user set-property --runtime \"$unit\" MemoryMax=256M MemorySwapMax=0 TasksMax=32 CPUQuota=50%\n" +
		"ready=" + managedEndpointPhysicalQuote(filepath.Join(fixture.instance.ServiceDir, "expiry-ready-")) + "\"$invocation\"\n" +
		managedEndpointPhysicalQuote(python) + " " + managedEndpointPhysicalQuote(timer) + " \"$$\" \"$unit\" \"$ready\" >/dev/null 2>&1 &\n" +
		"for attempt in 1 2 3 4 5 6 7 8 9 10; do test ! -s \"$ready\" || break; sleep 0.1; done\ntest -s \"$ready\"\n" +
		"exec " + managedEndpointPhysicalQuote(binary) + " \"$@\"\n"
	if err := os.WriteFile(fixture.wrapper, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer fixture.cancel()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := fixture.cleanupPhysical(cleanupCtx); err != nil {
			t.Errorf("exact private fixture cleanup: %v (instance=%s container=%s)", err, fixture.instance.ID, fixture.containerID)
		}
		for _, store := range fixture.stores {
			_ = store.Close()
		}
	})
	t.Logf("physical fixture instance=%s unit=%s port=%d runtime=%s image=%s; same-unit pidfd expiry TERM50s/KILL55s, HTTP children55s; no production ports", fixture.instance.ID, unit, registration.Port, fixture.instance.RuntimeRoot, managedEndpointPhysicalImage)
	return fixture
}

func (fixture *managedEndpointPhysicalFixture) coordinator(t *testing.T) (*compute.ManagedEndpointCoordinator, compute.ManagedEndpointOwnerRuntime) {
	t.Helper()
	runtime, err := compute.NewManagedEndpointSystemRuntime(fixture.instance.RuntimeRoot, fixture.wrapper, fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	return &compute.ManagedEndpointCoordinator{Store: fixture.store, Runtime: runtime, RuntimeRoot: fixture.instance.RuntimeRoot,
		Environment: func(_ context.Context, instance compute.ManagedEndpointOwnerInstance, _ string) (map[string]string, error) {
			return map[string]string{"HOST_PORT": strconv.Itoa(instance.Registration.Port), "SERVICE_DIR": instance.ServiceDir}, nil
		}}, runtime
}

func (fixture *managedEndpointPhysicalFixture) reload(t *testing.T, ref compute.ManagedEndpointActionRef) compute.ManagedEndpointAction {
	t.Helper()
	instance, action, err := fixture.store.ReadManagedEndpointAction(fixture.ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	fixture.instance = instance
	return action
}

func (fixture *managedEndpointPhysicalFixture) reopen(t *testing.T) {
	t.Helper()
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExisting(fixture.instance.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	fixture.store = reopened
	fixture.stores = append(fixture.stores, reopened)
}

func (fixture *managedEndpointPhysicalFixture) stop(t *testing.T) compute.ManagedEndpointOwnerReceipt {
	t.Helper()
	claimed, run, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot)
	if err != nil || !run {
		t.Fatalf("physical Stop claim=%t err=%v", run, err)
	}
	coordinator, _ := fixture.coordinator(t)
	state, receipt, err := coordinator.Run(fixture.ctx, managedEndpointOwnerTestClaim(claimed, fixture.registration))
	if err != nil || state != "stopped" || !receipt.HasPositiveStoppedProof(fixture.instance) {
		t.Fatalf("physical Stop state=%s receipt=%#v err=%v", state, receipt, err)
	}
	fixture.reload(t, receipt.Ref)
	return receipt
}

type managedEndpointPhysicalHealth struct {
	Owner  string `json:"owner"`
	Starts int    `json:"starts"`
	PID    int64  `json:"pid"`
}

func (fixture *managedEndpointPhysicalFixture) health(t *testing.T, starts int) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(fixture.registration.URL + fixture.registration.LivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value managedEndpointPhysicalHealth
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&value); err != nil || response.StatusCode != http.StatusOK || value.Owner != fixture.instance.ID || value.Starts != starts {
		t.Fatalf("foreign/missing/replayed physical response: status=%d value=%#v err=%v", response.StatusCode, value, err)
	}
}

func managedEndpointPhysicalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if port == 8765 || port == 8766 {
		t.Fatal("refuse to use a production port for a private physical fixture")
	}
	return port
}

func managedEndpointPhysicalQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type managedEndpointPhysicalCapture struct{ text []byte }

func (output *managedEndpointPhysicalCapture) Write(value []byte) (int, error) {
	length := len(value)
	if remain := (32 << 10) - len(output.text); remain > 0 {
		output.text = append(output.text, value[:min(remain, length)]...)
	}
	return length, nil
}

func managedEndpointPhysicalCommand(ctx context.Context, executable string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.WaitDelay = 200 * time.Millisecond
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	for _, name := range []string{"HOME", "XDG_RUNTIME_DIR"} {
		if value := os.Getenv(name); value != "" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	output := &managedEndpointPhysicalCapture{}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	return strings.TrimSpace(string(output.text)), errors.Join(context.Cause(ctx), err)
}

func managedEndpointPhysicalWait(ctx context.Context, check func() error) error {
	var last error
	for {
		if last = check(); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(context.Cause(ctx), last)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func managedEndpointPhysicalReadJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("private physical fixture record is invalid")
	}
	return json.NewDecoder(io.LimitReader(file, 4097)).Decode(target)
}

func (fixture *managedEndpointPhysicalFixture) cleanupPhysical(ctx context.Context) error {
	var failures []error
	if fixture.docker != "" {
		if err := fixture.cleanupDocker(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	// Re-read the actual admission record: a failed assertion may have happened
	// after the worker was accepted but before the caller received its receipt.
	if fixture.store != nil {
		if instance, _, err := fixture.store.ReadManagedEndpointAction(ctx, fixture.action.Ref); err == nil {
			fixture.instance = instance
		}
	}
	identity := fixture.instance.WorkerIdentity
	if fixture.instance.Identity.Kind == "native-unit" {
		identity = fixture.instance.Identity
	}
	if identity.Kind == "native-unit" {
		if err := fixture.stopPrivateWorker(ctx, identity); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func managedEndpointPhysicalRegistration(kind string, port int, start, stop string) compute.ManagedEndpointRegistration {
	return compute.ManagedEndpointRegistration{Name: fmt.Sprintf("private-%s-%d", kind, port), URL: fmt.Sprintf("http://127.0.0.1:%d", port), Port: port, SkillName: "using-model-endpoint", StartScript: start, StopScript: stop, LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{Version: compute.ManagedEndpointOwnerProtocol, Kind: kind, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
}
