package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/subprocess"
)

func stopHostProcesses(t *testing.T, host *Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := host.StopExternalProcesses(ctx); err != nil {
		t.Errorf("StopExternalProcesses: %v", err)
	}
}

// Delay only the wait goroutine registration, not process termination. This
// models a real reaper that has not yet been scheduled, without a mock Kill.
func ownedUnreapedProcess(t *testing.T, host *Host, id string) (*externalProcess, func()) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	command := exec.CommandContext(context.Background(), executable, "-test.run=^TestExternalPluginProcessHelper$")
	command.Env, err = subprocess.BuildEnvironment("plugin test", map[string]string{
		"X_SCIENCE_PLUGIN_PROCESS_HELPER": "1", "X_SCIENCE_PLUGIN_READY_FILE": ready,
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := subprocess.Prepare(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	process := &externalProcess{command: command, control: control, done: make(chan struct{})}
	var reapOnce sync.Once
	reap := func() { reapOnce.Do(func() { go host.waitForProcess(id, process) }) }
	t.Cleanup(func() {
		_ = control.Kill(command)
		_ = command.Process.Kill() // Attach may have failed before the parent entered its Job.
		reap()
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
			t.Error("controlled test process did not finish cleanup")
		}
	})
	if err := control.Attach(command); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	host.mu.Lock()
	host.plugins[id] = Plugin{ID: id, Runtime: RuntimeStatus{Kind: "external_process", Status: "running", PID: command.Process.Pid}}
	host.processes[id] = process
	host.mu.Unlock()
	return process, reap
}

func TestHostStopWaitsForReapingAndRejectsOverlappingStart(t *testing.T) {
	host := New(nil)
	process, reap := ownedUnreapedProcess(t, host, "owned")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.StopExternalProcesses(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("unreaped stop must report incomplete join: %v", err)
	}
	plugin, _ := host.Get("owned")
	if plugin.Runtime.Status != "stopping" || plugin.Runtime.PID == 0 {
		t.Fatalf("premature stopped projection: %#v", plugin.Runtime)
	}
	if err := host.StartExternalProcesses(context.Background()); err == nil {
		t.Fatal("replacement start accepted before prior Wait cleanup")
	}
	reap()
	stopHostProcesses(t, host)
	select {
	case <-process.done:
	default:
		t.Fatal("stop returned before reaper completion")
	}
	plugin, _ = host.Get("owned")
	if plugin.Runtime.Status != "stopped" || plugin.Runtime.PID != 0 || process.command.ProcessState == nil {
		t.Fatalf("missing actual reaping/projection: %#v", plugin.Runtime)
	}
	if err := host.StartExternalProcesses(context.Background()); err != nil {
		t.Fatalf("completed ownership was not released: %v", err)
	}
}

func TestHostStopCancelsAllBeforeDeadlineAndConcurrentCallersShareReaping(t *testing.T) {
	host := New(nil)
	first, reapFirst := ownedUnreapedProcess(t, host, "first")
	second, reapSecond := ownedUnreapedProcess(t, host, "second")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.StopExternalProcesses(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected incomplete join: %v", err)
	}
	for _, id := range []string{"first", "second"} {
		plugin, _ := host.Get(id)
		if plugin.Runtime.Status != "stopping" {
			t.Fatalf("%s was not cancelled before waiting: %#v", id, plugin.Runtime)
		}
	}
	var callers sync.WaitGroup
	for range 4 {
		callers.Go(func() { stopHostProcesses(t, host) })
	}
	reapFirst()
	reapSecond()
	callers.Wait()
	for _, process := range []*externalProcess{first, second} {
		if process.command.ProcessState == nil {
			t.Fatal("a process was not reaped")
		}
	}
}

func TestHostStopRetainsSettledInfrastructureFailureAndBlocksRestart(t *testing.T) {
	host := New(nil)
	command := exec.Command("not-started")
	control, err := subprocess.Prepare(command)
	if err != nil {
		t.Fatal(err)
	}
	process := &externalProcess{command: command, control: control, done: make(chan struct{})}
	host.plugins["broken"] = Plugin{ID: "broken"}
	host.processes["broken"] = process
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = host.StopExternalProcesses(ctx)
	host.waitForProcess("broken", process)
	if process.waitErr == nil {
		t.Fatal("Wait infrastructure failure was discarded")
	}
	plugin, _ := host.Get("broken")
	if plugin.Runtime.Status != "failed" || plugin.Runtime.Error == "" {
		t.Fatalf("cleanup failure was hidden: %#v", plugin.Runtime)
	}
	if err := host.StopExternalProcesses(ctx); err == nil || !errors.Is(err, process.waitErr) {
		t.Fatalf("settled cleanup failure disappeared from Stop: %v", err)
	}
	if err := host.StartExternalProcesses(context.Background()); err == nil {
		t.Fatal("failed cleanup authorized replacement")
	}
	// Successful cleanup, unlike an infrastructure failure, takes precedence
	// over an already cancelled budget.
	if err := New(nil).StopExternalProcesses(ctx); err != nil {
		t.Fatalf("completed empty host spuriously failed: %v", err)
	}
}
