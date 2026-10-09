package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
)

func TestStructurePreviewKernelIsRequestScopedAfterTaskCompletion(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	completed := workspace.FrameStatusCompleted
	if _, err := store.UpdateFrame(identity.access.Frame.ID, workspace.UpdateFrameInput{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	worker, err := app.startStructurePreviewKernel(kernelruntime.SessionSpec{
		KernelID: "kernel-preview", FrameID: identity.access.Frame.ID, RootFrameID: identity.access.Frame.RootFrameID,
		AgentName: "OPERON", KernelKind: "analysis", Language: "python", Environment: "python", WorkspaceDir: identity.workspaceDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		response, err := worker.Execute(ctx, "from pathlib import Path\nimport time\nPath('preview-running').write_text('ready')\nwhile not Path('preview-release').exists():\n    time.sleep(0.01)\nprint('preview-complete')", "agent")
		if err == nil && response.Error != "" {
			err = errors.New(response.Error)
		}
		done <- err
	}()
	marker := filepath.Join(identity.workspaceDir, "preview-running")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("preview calculation did not start")
	}
	if candidates := manager.SnapshotIdleCandidates(); len(candidates) != 0 {
		t.Fatalf("a running request-scoped preview is incorrectly exposed to the task idle reaper: %#v", candidates)
	}
	// Advance the task idle policy without waiting five seconds. Request-owned
	// previews must survive even when their completed parent task is expired.
	app.kernelIdleNow = func() time.Time { return time.Now().UTC().Add(time.Hour) }
	reaperContext, stopReaper := context.WithCancel(t.Context())
	reaperDone := make(chan error, 1)
	go func() { reaperDone <- app.RunKernelIdleReaper(reaperContext) }()
	defer stopReaper()
	if err := os.WriteFile(filepath.Join(identity.workspaceDir, "preview-release"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("request-scoped calculation failed: %v", err)
	}
	if err := app.closeAgentKernel("kernel-preview"); err != nil {
		t.Fatal(err)
	}
	stopReaper()
	if err := <-reaperDone; err != nil {
		t.Fatal(err)
	}
	if manager.ActiveCount() != 0 {
		t.Fatal("request-owned preview leaked a kernel after explicit cleanup")
	}
}

func TestStructurePreviewKernelCancelsAndReleasesRequestOwnedWorker(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	worker, err := app.startStructurePreviewKernel(kernelruntime.SessionSpec{
		KernelID: "kernel-preview-cancel", FrameID: identity.access.Frame.ID, RootFrameID: identity.access.Frame.RootFrameID,
		AgentName: "OPERON", KernelKind: "analysis", Language: "python", Environment: "python", WorkspaceDir: identity.workspaceDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := worker.Execute(ctx, "from pathlib import Path\nimport time\nPath('preview-cancel-running').write_text('ready')\nwhile True:\n    time.sleep(0.01)", "agent")
		done <- err
	}()
	marker := filepath.Join(identity.workspaceDir, "preview-cancel-running")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("preview calculation did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("preview cancellation lost its cause: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preview did not stop after its request was cancelled")
	}
	if err := app.closeAgentKernel("kernel-preview-cancel"); err != nil {
		t.Fatal(err)
	}
	if manager.ActiveCount() != 0 {
		t.Fatal("cancelled preview leaked a kernel")
	}
}
