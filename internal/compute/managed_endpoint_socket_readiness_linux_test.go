//go:build linux

package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A systemd MainPID can exist before exec/SQLite-open/socket-listen completes.
// Exercise that distinction without starting a real unit, container or script:
// only the finite control projection is fake, and the private Unix protocol is
// real. No request here is allowed to submit, accept or cancel an action.
func TestManagedEndpointWorkerWaitsForIdentifiedControlSocket(t *testing.T) {
	for _, scenario := range []string{"fresh_delayed_listener", "existing_delayed_listener", "foreign_socket_identity", "cancelled_wait"} {
		t.Run(scenario, func(t *testing.T) {
			runtime, instance := managedEndpointSocketFixture(t, scenario == "fresh_delayed_listener")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			expected, err := runtime.unit.observe(ctx, instance)
			if err != nil {
				t.Fatal(err)
			}
			// The fresh projection is initially absent; its controlled launcher
			// changes only its private state file to the same observed PID.
			if expected.Absent {
				expected.Identity = managedEndpointSocketIdentity(t, instance)
			}
			var protocol <-chan error
			if scenario == "foreign_socket_identity" {
				foreign := expected.Identity
				foreign.InvocationID = strings.Repeat("b", 32)
				protocol = managedEndpointSocketServer(t, instance, foreign)
			}
			requestCtx, stopRequest := context.WithCancelCause(ctx)
			defer stopRequest(nil)
			type result struct {
				observed managedEndpointUnitObservation
				err      error
			}
			finished := make(chan result, 1)
			go func() {
				observed, err := runtime.worker(requestCtx, instance, true, false)
				finished <- result{observed: observed, err: err}
			}()
			if scenario == "foreign_socket_identity" {
				outcome := <-finished
				if !errors.Is(outcome.err, ErrManagedEndpointOwnershipUnknown) {
					t.Fatalf("foreign control socket was treated as the worker: %#v err=%v", outcome.observed, outcome.err)
				}
				if err := <-protocol; err != nil {
					t.Fatal(err)
				}
				return
			}
			select {
			case outcome := <-finished:
				t.Fatalf("PID-only observation returned before the control socket existed: %#v err=%v", outcome.observed, outcome.err)
			case <-time.After(100 * time.Millisecond):
			}
			if scenario == "cancelled_wait" {
				cause := errors.New("controlled observer closed while socket was not ready")
				stopRequest(cause)
				select {
				case outcome := <-finished:
					if !errors.Is(outcome.err, cause) {
						t.Fatalf("socket wait lost cancellation cause: %v", outcome.err)
					}
				case <-time.After(time.Second):
					t.Fatal("socket wait ignored its existing context bound")
				}
				return
			}
			protocol = managedEndpointSocketServer(t, instance, expected.Identity)
			outcome := <-finished
			if outcome.err != nil || outcome.observed.Identity != expected.Identity {
				t.Fatalf("delayed exact worker was not confirmed: %#v err=%v", outcome.observed, outcome.err)
			}
			if err := <-protocol; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func managedEndpointSocketFixture(t *testing.T, fresh bool) (*ManagedEndpointSystemRuntime, ManagedEndpointOwnerInstance) {
	t.Helper()
	// Keep the AF_UNIX address below108 bytes without borrowing another
	// application's runtime directory. Remove only this exact MkdirTemp result.
	rpcRoot, err := os.MkdirTemp("/tmp", "xsci-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rpcRoot) })
	t.Setenv("XDG_RUNTIME_DIR", rpcRoot)
	root := t.TempDir()
	registration := ManagedEndpointRegistration{Name: "socket-fixture", URL: "http://127.0.0.1:25001", Port: 25001, LivePath: "/health", StartScript: "not executed", StopScript: "not executed", Owner: &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	instance := ManagedEndpointOwnerInstance{ID: "socket-fixture", UserID: "private-owner", Endpoint: registration.Name, Registration: registration, ApprovedHash: ApprovedManagedEndpointHash(registration), StartGeneration: 1, RuntimeRoot: root, ServiceDir: t.TempDir(), WorkspacePath: filepath.Join(t.TempDir(), "workspace.db"), InitialAction: ManagedEndpointActionRef{InstanceID: "socket-fixture", Kind: "start", ClaimHandle: "original", Generation: 1}}
	identity := managedEndpointSocketIdentity(t, instance)
	state := filepath.Join(root, "unit-active")
	if !fresh {
		if err := os.WriteFile(state, []byte("controlled active"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	control := filepath.Join(root, "private-systemctl")
	script := "#!/bin/sh\nif ! test -f " + quote(state) + "; then printf 'LoadState=not-found\\nActiveState=inactive\\n'; exit 0; fi\n" +
		fmt.Sprintf("printf 'LoadState=loaded\\nActiveState=active\\nInvocationID=%s\\nControlGroup=%s\\nMainPID=%d\\n'\n", identity.InvocationID, identity.ControlGroup, identity.PID)
	if err := os.WriteFile(control, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "private-systemd-run")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf 'controlled active' > "+quote(state)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ManagedEndpointSystemRuntime{root: root, executable: "/private-cli-not-executed", unit: &managedEndpointUnit{run: launcher, control: control}}, instance
}

func managedEndpointSocketIdentity(t *testing.T, instance ManagedEndpointOwnerInstance) ManagedEndpointRuntimeIdentity {
	t.Helper()
	ticks, state, err := managedEndpointProcessTicks(int64(os.Getpid()))
	if err != nil || state == "Z" || state == "X" {
		t.Fatalf("controlled process identity unavailable: %v", err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	return ManagedEndpointRuntimeIdentity{Kind: "native-unit", BootID: strings.TrimSpace(string(boot)), Unit: ManagedEndpointUnitName(instance.ID), InvocationID: strings.Repeat("a", 32), ControlGroup: "/private-socket-fixture", PID: int64(os.Getpid()), StartTicks: ticks}
}

func managedEndpointSocketServer(t *testing.T, instance ManagedEndpointOwnerInstance, identity ManagedEndpointRuntimeIdentity) <-chan error {
	t.Helper()
	path, err := managedEndpointSocketPath(instance)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	finished := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		var request managedEndpointWorkerRequest
		if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&request); err != nil {
			finished <- err
			return
		}
		if request.Version != ManagedEndpointOwnerProtocol || request.InstanceID != instance.ID || request.Method != "hello" || request.Action != (ManagedEndpointAction{}) || len(request.Environment) != 0 {
			finished <- errors.New("worker readiness attempted a mutating or foreign request")
			return
		}
		finished <- json.NewEncoder(connection).Encode(managedEndpointWorkerResponse{Version: ManagedEndpointOwnerProtocol, InstanceID: instance.ID, WorkerIdentity: identity})
	}()
	return finished
}
