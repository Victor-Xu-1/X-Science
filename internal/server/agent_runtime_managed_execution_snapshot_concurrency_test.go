package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedExecutionSnapshotLockWaitIsCancellable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "results")
	lock := agentWorkspaceEditLock("managed-execution-snapshot:\x00" + root)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, returned := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		unlock, err := lockManagedExecutionSnapshot(ctx, root)
		if unlock != nil {
			unlock()
		}
		returned <- err
	}()
	<-started
	select {
	case err := <-returned:
		t.Fatalf("live waiter escaped held snapshot lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("held snapshot lock ignored cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled snapshot waiter remained blocked by the publisher")
	}
}

func TestManagedExecutionAuthorityWaitsForSnapshotPublication(t *testing.T) {
	for _, verifyOnly := range []bool{false, true} {
		name := "verify_and_publish"
		if verifyOnly {
			name = "direct_receipt_verification"
		}
		t.Run(name, func(t *testing.T) { testManagedExecutionPublicationWait(t, verifyOnly) })
	}
}

func testManagedExecutionPublicationWait(t *testing.T, verifyOnly bool) {
	f := newAgentSaveArtifactsFixture(t)
	executionInputAuthorityFixture(t, f.server, f.projectPath)
	inputAuthorityReceipt(t, f, "results", "producer-one", "ok", "")
	records, err := f.store.ListExecutionLog(f.identity.access.Frame.ID, "")
	if err != nil || len(records) != 1 {
		t.Fatalf("execution records: %v %v", records, err)
	}
	bindings, err := f.store.KernelLocalExecutionBindings(context.Background(), f.identity.access)
	if err != nil {
		t.Fatal(err)
	}
	writes, err := managedExecutionReceiptWriteMap(f.projectPath, records[0].FilesWritten)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(f.projectPath, "results")
	authority, err := f.server.verifyManagedExecutionOutputAuthority(context.Background(), f.projectPath,
		output, "analysis.producer", records[0].ID, writes)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the legitimate publisher's temporary compatibility-path gap
	// while holding its existing per-output lock. A reader must wait, not
	// classify verified bytes as missing/unsafe and ask for recomputation.
	lock := agentWorkspaceEditLock("managed-execution-snapshot:\x00" + output)
	lock.Lock()
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	detached := filepath.Join(f.projectPath, "publication-in-progress")
	if err := os.Rename(output, detached); err != nil {
		t.Fatal(err)
	}
	type result struct {
		authorities []managedExecutionOutputAuthority
		err         error
	}
	started, returned := make(chan struct{}), make(chan result, 1)
	go func() {
		close(started)
		if verifyOnly {
			item, err := f.server.verifyManagedExecutionOutputAuthority(context.Background(), f.projectPath,
				output, "analysis.producer", records[0].ID, writes)
			returned <- result{[]managedExecutionOutputAuthority{item}, err}
			return
		}
		items, err := f.server.managedExecutionOutputAuthorities(context.Background(), f.identity.access,
			f.projectPath, records, bindings)
		returned <- result{items, err}
	}()
	<-started
	select {
	case got := <-returned:
		t.Fatalf("authority escaped the publication lock: %#v %v", got.authorities, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	final := managedExecutionSnapshotDirectory(f.projectPath, authority)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(detached, final); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(managedExecutionSnapshotManifest{Schema: managedExecutionSnapshotSchema,
		PackID: authority.PackID, ExecutionID: authority.ExecutionID, Digests: authority.Digests})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(final, ".synon-output-snapshot.json"), manifest, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(final, output); err != nil {
		t.Fatal(err)
	}
	lock.Unlock()
	locked = false
	select {
	case got := <-returned:
		if got.err != nil || len(got.authorities) != 1 || got.authorities[0].Unavailable || got.authorities[0].ResolvedRoot != final {
			t.Fatalf("publication was not recovered as immutable authority: %#v %v", got.authorities, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("authority reader did not resume after publication")
	}
}
