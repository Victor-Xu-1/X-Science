//go:build unix

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	taskstore "synon-go/internal/persistence/tasks"
	"synon-go/internal/tools/shellops"
)

func TestServerCloseStopsBackgroundShellProcessTreeAndTask(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	result, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command":           "sleep 60 & echo $! > shutdown-child.pid; wait",
		"workdir":           ".",
		"description":       "shutdown process tree fixture",
		"run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	value := mapValue(result)
	taskID := stringValue(value["task_id"])
	if taskID == "" {
		t.Fatalf("background shell result = %#v", value)
	}
	pid := waitForServerShellChildPID(t, filepath.Join(root, "shutdown-child.pid"))
	if !serverTestProcessExists(pid) {
		t.Fatalf("background child process %d was not running", pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for serverTestProcessExists(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if serverTestProcessExists(pid) {
		t.Fatalf("background child process %d survived Server.Close", pid)
	}
	task, found, err := srv.taskStore.Get(taskID)
	if err != nil || !found || task.Status != "stopped" || task.Metadata["stoppedBy"] != "server_shutdown" {
		t.Fatalf("shutdown task = %#v found=%v error=%v", task, found, err)
	}
	srv.backgroundShellMu.Lock()
	remaining := len(srv.backgroundShells)
	srv.backgroundShellMu.Unlock()
	if remaining != 0 {
		t.Fatalf("background shell registry retained %d entries", remaining)
	}
}

func waitForServerShellChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background child PID file %s was not created", path)
	return 0
}

func serverTestProcessExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func TestServerCloseWaitsForBackgroundShellSettlement(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	result, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command": "sleep 60", "workdir": ".", "run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	value := mapValue(result)
	taskID := stringValue(value["task_id"])
	output := filepath.Join(root, stringValue(value["output_path"]))
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	// A real FIFO holds the final log write after process exit. Process death
	// alone cannot satisfy shutdown while terminal persistence is outstanding.
	if err := syscall.Mkfifo(output, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	closeErr := srv.Close(ctx)
	cancel()
	reader, err := os.OpenFile(output, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, found, err := srv.taskStore.Get(taskID)
		if err != nil || !found {
			t.Fatalf("settlement task: found=%t err=%v", found, err)
		}
		if stringValue(task.Metadata["completedAt"]) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background settlement did not finish after releasing log writer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Errorf("Close returned before blocked settlement completed: %v", closeErr)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestServerCloseRejectsNewBackgroundShellAdmission(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command": "touch after-close.txt", "workdir": ".", "run_in_background": true,
	}); err == nil {
		t.Fatal("closed server admitted a new background process")
	}
	if _, err := os.Stat(filepath.Join(root, "after-close.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-shutdown command changed the filesystem: %v", err)
	}
}

func TestBackgroundShellFailedStartReleasesShutdownReservation(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	if _, err := srv.startBackgroundShellCommand("unsupported-shell", "", "", map[string]any{"command": "exit"}); err == nil {
		t.Fatal("unsupported shell started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatalf("failed launch retained its lifetime reservation: %v", err)
	}
}

func TestBackgroundShellLateRegistrationStopsAndSettles(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	if !srv.admitBackgroundShell() {
		t.Fatal("initial launch was refused")
	}
	task, err := srv.taskStore.CreateWithOptions(taskstore.CreateOptions{Subject: "late launch", Status: "running"})
	if err != nil {
		srv.backgroundShellWG.Done()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	closeErr := srv.Close(ctx)
	cancel()
	running, err := shellops.StartShellCommand(context.Background(), root, "Bash", "sleep 60", ".")
	if err != nil {
		srv.backgroundShellWG.Done()
		t.Fatal(err)
	}
	srv.storeBackgroundShell(task.ID, running)
	go srv.finishBackgroundShell(task.ID, filepath.Join(root, "late.log"), running)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Fatalf("Close did not wait for the admitted launch: %v", closeErr)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, _, err := srv.taskStore.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stringValue(current.Metadata["completedAt"]) != "" {
			if current.Status != "stopped" || current.Metadata["stoppedBy"] != "server_shutdown" {
				t.Fatalf("late launch terminal = %+v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late registration survived the shutdown barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerCloseReportsBackgroundShellPersistenceFailure(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	result, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command": "sleep 60", "workdir": ".", "run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, stringValue(mapValue(result)["output_path"]))
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(ctx); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("terminal log failure was hidden: %v", err)
	}
}
