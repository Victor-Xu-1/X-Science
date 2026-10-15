//go:build linux

package compute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This is the captured filesystem writer race only, not a SQL retirement,
// Delete/outbox, physical-unit-stop or scientific-execution witness. The real
// cleanup helper receives an exact descriptor-valid scope in test-owned paths.
func TestManagedEndpointCapturedLaunchDoesNotRepublishCleanedPrivateScope(t *testing.T) {
	runtimeRoot := filepath.Join(t.TempDir(), "owners")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	instance := managedEndpointPrivateCleanupRootTestInstance(t, runtimeRoot)
	directory := managedEndpointInstanceDirectory(instance)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedEndpointPrivateJSON(filepath.Join(directory, "owner.json"), instance); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedEndpointPrivateJSON(filepath.Join(directory, "action.json"), ManagedEndpointAction{Ref: instance.InitialAction}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "output-0"), []byte("test-owned output"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Keep this fixture's AF_UNIX locator short. XDG socket parent creation is a
	// separate contract: cleanup neither removes nor fences that parent directory.
	rpcRoot, err := os.MkdirTemp("/tmp", "xsci-publication-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rpcRoot) })
	t.Setenv("XDG_RUNTIME_DIR", rpcRoot)
	socket, err := managedEndpointSocketPath(instance)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{directory, filepath.Join(directory, "owner.json"), filepath.Join(directory, "action.json"), filepath.Join(directory, "output-0"), socket}
	ctx, cancel := context.WithCancel(t.Context())
	ready, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var launchErr error
	go func(captured ManagedEndpointOwnerInstance) {
		defer close(finished)
		close(ready)
		<-release
		// /bin/true ignores the private CLI arguments. No actual systemd unit,
		// worker CLI, Store acceptance, approved Start/Stop script or task runs.
		launchErr = (&managedEndpointUnit{run: "/bin/true"}).launch(ctx, captured, captured.InitialAction, "/private-cli-not-executed")
	}(instance)
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Error("captured test writer did not stop within its cleanup bound")
		}
	})
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("test writer did not capture its original instance")
	}
	scope, err := ManagedEndpointPrivateCleanupFor(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := CleanupManagedEndpointPrivateFiles(ctx, runtimeRoot, scope); err != nil {
		t.Fatalf("exact test-owned private scope cleanup: %v", err)
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fixture was not absent before releasing the captured writer: %q %v", path, err)
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("captured test writer did not return within its control-command bound")
	}
	t.Logf("captured leaf launch returned %v; no managed-owner publication is allowed after cleanup", launchErr)
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("captured old launch republished a cleaned private owner path: %q %v", path, err)
		}
	}
}
