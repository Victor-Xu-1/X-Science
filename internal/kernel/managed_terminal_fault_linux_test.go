//go:build linux

package kernel

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestManagedOneShotCancellationAndReaperLossAreObservable(t *testing.T) {
	for _, mode := range []string{"cancel", "reaper_lost"} {
		t.Run(mode, func(t *testing.T) { verifyManagedTerminalFault(t, mode) })
	}
}

func verifyManagedTerminalFault(t *testing.T, mode string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifetimeRead, lifetimeWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifetimeRead.Close(); _ = lifetimeWrite.Close() })
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	command := newWorkerProcessCommand(ctx, os.Args[0], "-test.run=^TestManagedTerminalProcessHelper$", "-test.timeout=15s")
	command.Env = []string{managedTerminalHelperEnv + "=root", "X_SCIENCE_TEST_TERMINAL_PID=" + pidPath,
		"X_SCIENCE_TEST_TERMINAL_DETACHED=true", "X_SCIENCE_TEST_TERMINAL_OUTPUT=true"}
	// Neither reader consumes data. The fixture owns the only writer, so EOF
	// can independently release its exact child even if the reaper is lost.
	command.Stdin = lifetimeRead
	command.ExtraFiles = []*os.File{lifetimeRead}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	process, err := startWorkerProcess(command)
	if err != nil {
		t.Fatal(err)
	}
	_ = lifetimeRead.Close()
	waited := make(chan struct{})
	var result error
	go func() { result = command.Wait(); process.close(); close(waited) }()
	t.Cleanup(func() {
		cancel()
		_ = command.Process.Kill() // The test's retained helper handle only.
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			t.Error("fixture observation did not return")
		}
	})
	childPID := waitForWorkerProcessChildPID(t, pidPath, 3*time.Second)
	startBytes, err := os.ReadFile(pidPath + ".start")
	if err != nil {
		t.Fatal(err)
	}
	start, err := strconv.ParseInt(string(startBytes), 10, 64)
	if err != nil || start <= 0 {
		t.Fatal("fixture child birth identity is invalid")
	}
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = lifetimeWrite.Close()
		defer child.Release()
		// The race runtime delays os.Exit(0) after the child has received EOF.
		// This fixture teardown budget is separate from the production-result
		// assertions above; no task or process-settlement standard is relaxed.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			alive, err := ProcessIdentityAlive(int64(childPID), start)
			if err == nil && !alive {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Error("owned fixture child did not acknowledge EOF; its 12s self-deadline remains")
	})
	alive, err := ProcessIdentityAlive(int64(childPID), start)
	if err != nil || !alive {
		t.Fatalf("fixture child no longer matches its captured identity: %v", err)
	}
	if mode == "cancel" {
		cancel()
	} else if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("caller remained blocked after cancellation or loss of its reaper")
	}
	var cleanup *WorkerProcessCleanupError
	if mode == "reaper_lost" {
		if !errors.As(result, &cleanup) || cleanup.State != "cleanup_unknown" {
			t.Fatalf("lost reaper was not explicitly unknown: %v", result)
		}
	} else {
		alive, err := ProcessIdentityAlive(int64(childPID), start)
		if result == nil || errors.As(result, &cleanup) || err != nil || alive {
			t.Fatalf("ordinary cancellation did not physically settle: result=%v alive=%t error=%v", result, alive, err)
		}
	}
}
