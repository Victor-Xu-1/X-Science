package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/subprocess"
)

type startupBlockedOutput struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (output *startupBlockedOutput) Write(data []byte) (int, error) {
	output.once.Do(func() { close(output.started) })
	<-output.release
	return len(data), nil
}

func TestPluginStartupProcessHelper(t *testing.T) {
	if os.Getenv("X_SCIENCE_PLUGIN_STARTUP_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "controlled startup")
	for {
		time.Sleep(time.Second)
	}
}

func TestHostFailedStartupRetainsOwnershipUntilUniqueReaperCompletes(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint("cancelled=", cancelled), func(t *testing.T) {
			host := New(nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, executable, "-test.run=^TestPluginStartupProcessHelper$")
			command.Env, err = subprocess.BuildEnvironment("startup test", map[string]string{"X_SCIENCE_PLUGIN_STARTUP_HELPER": "1"})
			if err != nil {
				t.Fatal(err)
			}
			command.WaitDelay = 2 * time.Second
			output := &startupBlockedOutput{started: make(chan struct{}), release: make(chan struct{})}
			command.Stdout = output
			control, err := subprocess.Prepare(command)
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				_ = control.Close()
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			var process *externalProcess
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(output.release) })
				if process != nil {
					stopHostProcesses(t, host)
				} else {
					_ = control.Kill(command)
					_ = command.Process.Kill()
					_ = command.Wait()
					_ = control.Close()
				}
			})
			select {
			case <-output.started:
			case <-time.After(5 * time.Second):
				t.Fatal("controlled subprocess did not write startup output")
			}
			host.mu.Lock()
			process = host.ownStartedProcess("failed-start", Plugin{ID: "failed-start"}, command, control)
			ownedBeforeAttach := host.processes["failed-start"] == process && process.done != nil
			// Attachment refusal is a controlled startup failure, not a fake Kill
			// or Wait. The cancel case uses actual attached process cancellation.
			startErr := errors.New("controlled attachment refusal")
			if cancelled {
				startErr = control.Attach(command)
				cancel()
				if startErr == nil {
					startErr = ctx.Err()
				}
			}
			actual := host.finishProcessStartup("failed-start", process, startErr)
			host.mu.Unlock()
			if !ownedBeforeAttach || !errors.Is(actual, startErr) {
				t.Fatal("started process did not retain immutable ownership/start error")
			}
			stopped, stop := context.WithCancel(context.Background())
			stop()
			if err := host.StopExternalProcesses(stopped); !errors.Is(err, context.Canceled) {
				t.Fatalf("blocked real Wait was not a bounded incomplete join: %v", err)
			}
			if err := host.StartExternalProcesses(context.Background()); err == nil {
				t.Fatal("replacement admitted while startup-failure reaper was unfinished")
			}
			releaseOnce.Do(func() { close(output.release) })
			stopHostProcesses(t, host)
			plugin, _ := host.Get("failed-start")
			if plugin.Runtime.Status != "failed" || plugin.Runtime.PID != 0 || !strings.Contains(plugin.Runtime.Error, startErr.Error()) {
				t.Fatalf("startup failure was replaced by a stopped/success projection: %#v", plugin.Runtime)
			}
			if process.command.ProcessState == nil || process.waitErr != nil || host.shutdownErr != nil {
				t.Fatalf("expected one actual reap without inventing cleanup failure: wait=%v sticky=%v", process.waitErr, host.shutdownErr)
			}
			if err := host.StopExternalProcesses(stopped); err != nil {
				t.Fatalf("completed cleanup should not repeat startup failure: %v", err)
			}
		})
	}
}

func TestHostPartialStartupStopsStartedPluginsAfterAnotherStartFails(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	host := New([]Plugin{
		{ID: "started", Server: ServerManifest{Process: &ProcessManifest{
			Command: executable, Args: []string{"-test.run=^TestExternalPluginProcessHelper$"},
			Env: map[string]string{"X_SCIENCE_PLUGIN_PROCESS_HELPER": "1", "X_SCIENCE_PLUGIN_READY_FILE": filepath.Join(t.TempDir(), "ready")},
		}}},
		{ID: "refused", Server: ServerManifest{Process: &ProcessManifest{Command: filepath.Join(t.TempDir(), "missing")}}},
	})
	defer stopHostProcesses(t, host)
	if err := host.StartExternalProcesses(context.Background()); err == nil || !strings.Contains(err.Error(), "start plugin refused") {
		t.Fatalf("missing actual process-start refusal: %v", err)
	}
	stopHostProcesses(t, host)
	started, _ := host.Get("started")
	refused, _ := host.Get("refused")
	if started.Runtime.Status != "stopped" || started.Runtime.PID != 0 || refused.Runtime.Status != "failed" || host.shutdownErr != nil {
		t.Fatalf("partial-start cleanup lost status/ownership: started=%#v refused=%#v sticky=%v", started.Runtime, refused.Runtime, host.shutdownErr)
	}
}
