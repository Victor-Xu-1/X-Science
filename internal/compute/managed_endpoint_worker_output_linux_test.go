//go:build linux

package compute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The physical-control projection is the existing private socket fixture; no
// real unit or container is started. The real worker, pipe, finite approved
// shell, admission boundary and Unix protocol prove only that unknown output
// continuity does not prevent a newly authorized Stop action from executing.
// Physical owner/Stop-proof correctness remains the runtime's separate check.
func TestManagedEndpointWorkerOutputUnknownDoesNotBlockAuthorizedStop(t *testing.T) {
	for _, private := range []bool{true, false} {
		name := "history_unknown_drains_without_rewriting"
		if !private {
			name = "non_private_output_directory_stays_hard"
		}
		t.Run(name, func(t *testing.T) {
			runtime, instance := managedEndpointSocketFixture(t, false, false)
			// Enough finite pipe output to observe a drain before shell exit;
			// its bounded preview is not a second persistent log or epoch.
			instance.Registration.StopScript = "i=0; while [ \"$i\" -lt 512 ]; do printf '%s' '" + strings.Repeat("z", 4096) + "'; i=$((i+1)); done"
			instance.ApprovedHash = ApprovedManagedEndpointHash(instance.Registration)
			instance.Identity = managedEndpointSocketIdentity(t, instance)
			instance.WorkerIdentity = instance.Identity
			instanceDir := filepath.Join(instance.RuntimeRoot, instance.ID)
			if err := os.Mkdir(instanceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			outputDir := filepath.Join(instanceDir, "output")
			output, err := newManagedEndpointOutput(outputDir, instance.Registration.Owner.Output)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := output.Write([]byte(strings.Repeat("a", 4096) + strings.Repeat("b", 1024))); err != nil {
				_ = output.Close()
				t.Fatal(err)
			}
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outputDir, "cursor.json"), []byte(`{"version":`), 0o600); err != nil {
				t.Fatal(err)
			}
			if !private {
				if err := os.Chmod(outputDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			before := managedEndpointOutputDirectoryBytes(t, outputDir)
			descriptor := filepath.Join(instanceDir, "owner.json")
			if err := writeManagedEndpointPrivateJSON(descriptor, instance); err != nil {
				t.Fatal(err)
			}
			// RunManagedEndpointOwnerWorker resolves these exact fixture programs
			// itself. Neither fake permits an actual host service operation.
			bin := filepath.Join(t.TempDir(), "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			control, err := os.ReadFile(runtime.unit.control)
			if err != nil {
				t.Fatal(err)
			}
			for tool, body := range map[string]string{"systemctl": string(control), "systemd-run": "#!/bin/sh\nexit 93\n"} {
				if err := os.WriteFile(filepath.Join(bin, tool), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			stop := ManagedEndpointAction{Ref: ManagedEndpointActionRef{InstanceID: instance.ID, Kind: "stop", Generation: 2, ClaimHandle: "explicit-stop"}}
			store := &managedEndpointOutputWorkerStore{instance: instance, stop: stop.Ref}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			input, err := ManagedEndpointBootstrapFor(instance, stop.Ref)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- RunManagedEndpointOwnerWorker(ctx, input, store) }()
			if !private {
				select {
				case err := <-finished:
					if err == nil || store.accepts.Load() != 0 {
						t.Fatalf("non-private output was downgraded: err=%v accepts=%d", err, store.accepts.Load())
					}
				case <-ctx.Done():
					t.Fatal("non-private output did not reject bootstrap")
				}
				managedEndpointAssertOutputDirectoryUnchanged(t, outputDir, before)
				return
			}
			// Observe only the fixture's already-active unit. No launch or
			// replacement permission is granted while waiting for this hello.
			observed := managedEndpointUnitObservation{Identity: instance.WorkerIdentity, State: "active"}
			if _, err := runtime.awaitWorkerControl(ctx, instance, observed); err != nil {
				t.Fatalf("historical output blocked worker bootstrap: %v", err)
			}
			environment := map[string]string{"HOST_PORT": strconv.Itoa(instance.Registration.Port), "SERVICE_DIR": instance.ServiceDir}
			if _, err := runtime.call(ctx, instance, observed, "submit", stop, environment); err != nil {
				t.Fatal(err)
			}
			receipt, err := runtime.wait(ctx, instance, observed, stop)
			if err != nil || !receipt.Accepted || !receipt.ProcessExited || receipt.ExitCode == nil || *receipt.ExitCode != 0 || receipt.Failure != "" || store.accepts.Load() != 1 {
				t.Fatalf("authorized finite Stop was blocked by output continuity: receipt=%#v accepts=%d err=%v", receipt, store.accepts.Load(), err)
			}
			if !strings.Contains(receipt.OutputWarning, "unknown") || !strings.Contains(receipt.OutputWarning, "cumulative") || !strings.Contains(receipt.OutputWarning, "not persisted") {
				t.Errorf("receipt concealed unknown cumulative output and unpersisted drain: %q", receipt.OutputWarning)
			}
			if receipt.OutputSequence == 0 || receipt.OutputDroppedBytes == 0 || receipt.Transcript == "" {
				t.Errorf("unverified drain did not report current-reader observations: sequence=%d dropped=%d preview=%d", receipt.OutputSequence, receipt.OutputDroppedBytes, len(receipt.Transcript))
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("worker did not retain ordinary observer cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("private worker did not close after the fixture ended")
			}
			managedEndpointAssertOutputDirectoryUnchanged(t, outputDir, before)
		})
	}
}

type managedEndpointOutputWorkerStore struct {
	managedEndpointWorkerGateStore
	instance ManagedEndpointOwnerInstance
	stop     ManagedEndpointActionRef
	accepts  atomic.Int64
}

func (store *managedEndpointOutputWorkerStore) WithManagedEndpointBootstrapPublication(_ context.Context, input ManagedEndpointBootstrap, identity ManagedEndpointRuntimeIdentity, publish func(ManagedEndpointOwnerInstance, ManagedEndpointAction) error) error {
	// This Store projection belongs only to the existing output/protocol test;
	// the separate real two-Store regression exercises SQLite authorization.
	if input.RequestedAction != store.stop || !input.Matches(store.instance) || identity != store.instance.WorkerIdentity {
		return ErrManagedEndpointStaleAction
	}
	return publish(store.instance, ManagedEndpointAction{Ref: store.stop, Accepted: store.accepts.Load() != 0})
}

func (store *managedEndpointOutputWorkerStore) ReadManagedEndpointAction(_ context.Context, ref ManagedEndpointActionRef) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error) {
	if ref == store.instance.InitialAction {
		return store.instance, ManagedEndpointAction{Ref: ref, Accepted: true}, nil
	}
	if ref != store.stop {
		return ManagedEndpointOwnerInstance{}, ManagedEndpointAction{}, ErrManagedEndpointStaleAction
	}
	return store.instance, ManagedEndpointAction{Ref: ref, Accepted: store.accepts.Load() != 0}, nil
}

func (store *managedEndpointOutputWorkerStore) AcceptManagedEndpointAction(_ context.Context, ref ManagedEndpointActionRef, identity ManagedEndpointRuntimeIdentity) (bool, error) {
	if ref != store.stop || identity != store.instance.WorkerIdentity {
		return false, ErrManagedEndpointStaleAction
	}
	return store.accepts.CompareAndSwap(0, 1), nil
}

func managedEndpointAssertOutputDirectoryUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	if after := managedEndpointOutputDirectoryBytes(t, dir); !reflect.DeepEqual(before, after) {
		t.Error("worker fallback truncated, appended or rewrote historical output evidence")
	}
}
