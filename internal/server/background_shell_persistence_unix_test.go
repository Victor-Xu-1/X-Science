//go:build unix

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBackgroundShellFailedCommandDoesNotFailServerClose(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	result, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command": "exit 7", "workdir": ".", "run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := stringValue(mapValue(result)["task_id"])
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, found, err := srv.taskStore.Get(taskID)
		if err != nil || !found {
			t.Fatalf("task: found=%t err=%v", found, err)
		}
		if task.Status == "failed" {
			if numberValue(task.Metadata["exitCode"]) != 7 {
				t.Fatalf("failed exit code was lost: %+v", task)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("nonzero command never became failed: %+v", task)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatalf("ordinary command failure poisoned server shutdown: %v", err)
	}
}

func TestServerCloseDeadlineSurvivesBackgroundTaskStoreIO(t *testing.T) {
	root := t.TempDir()
	srv := New(Options{FileRoot: root})
	result, err := srv.executeShellTool(context.Background(), "Bash", map[string]any{
		"command": "sleep 60", "workdir": ".", "run_in_background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := stringValue(mapValue(result)["task_id"])
	path := filepath.Join(root, "tasks.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	srv.backgroundShellMu.Lock()
	running := srv.backgroundShells[taskID]
	srv.backgroundShellMu.Unlock()
	if running == nil {
		t.Fatal("background process was not registered")
	}
	if err := running.Kill(); err != nil {
		t.Fatal(err)
	}
	// Opening the writer only succeeds once the real terminal reader arrives.
	// Withhold bytes so task-store I/O remains blocked during Close.
	var writer *os.File
	deadline := time.Now().Add(3 * time.Second)
	for writer == nil {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer = os.NewFile(uintptr(fd), path)
			break
		}
		if !errors.Is(err, syscall.ENXIO) || time.Now().After(deadline) {
			t.Fatalf("terminal reader did not reach task store: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- srv.Close(ctx) }()
	var closeErr error
	returned := false
	select {
	case closeErr = <-closed:
		returned = true
	case <-time.After(time.Second):
	}
	// Restore the pathname before releasing the held reader, so later terminal
	// updates use the ordinary file while the first read finishes on the FIFO.
	replacement := path + ".restore"
	if err := os.WriteFile(replacement, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !returned {
		select {
		case closeErr = <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("Close did not recover after releasing task-store I/O")
		}
	}
	if !returned || !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Errorf("task-store I/O prevented the shutdown deadline: early=%t err=%v", returned, closeErr)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
