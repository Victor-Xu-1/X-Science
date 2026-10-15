package workspace

import (
	"context"
	"errors"
	"testing"

	"x-science/internal/compute"
)

// This is the actual SQLite intent scan and coordinator; only OS execution is
// controlled. A failed dependency on the first ref must not restart the scan
// before a different endpoint gets its original, unaccepted intent dispatched.
func TestManagedEndpointEnvironmentFailureDoesNotStarvePendingIntents(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing credential", true: "cancelled before admission"}[cancelFirst], func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			addManagedEndpointFairnessIntent(t, fixture)
			refs, err := fixture.store.PendingManagedEndpointActions(context.Background(), nil, 8)
			if err != nil || len(refs) != 2 {
				t.Fatalf("private pending refs=%#v err=%v", refs, err)
			}
			blocked, healthy := refs[0], refs[1]
			if cancelFirst {
				if won, err := fixture.store.CancelManagedEndpointAction(context.Background(), blocked); err != nil || !won {
					t.Fatalf("private cancellation=%t err=%v", won, err)
				}
			}
			runtime := &managedOwnerFairnessRuntime{managedOwnerRecoveryRuntime: &managedOwnerRecoveryRuntime{store: fixture.store}}
			coordinator := newManagedOwnerRecoveryCoordinator(fixture, runtime.managedOwnerRecoveryRuntime)
			coordinator.Runtime = runtime
			blockedResolutions := 0
			coordinator.Environment = func(_ context.Context, instance compute.ManagedEndpointOwnerInstance, _ string) (map[string]string, error) {
				if instance.ID == blocked.InstanceID {
					blockedResolutions++
					return nil, errors.New("controlled approved credential unavailable")
				}
				return map[string]string{"HOST_PORT": "25001", "SERVICE_DIR": instance.ServiceDir}, nil
			}
			if _, err := coordinator.Reconcile(context.Background(), nil, 8); err != nil {
				t.Fatalf("one dependency restarted the whole observer: %v", err)
			}
			_, completed, err := fixture.store.ReadManagedEndpointAction(context.Background(), healthy)
			if err != nil || completed.Receipt == nil || !completed.Receipt.Readiness || runtime.startCalls != 1 {
				t.Fatalf("healthy endpoint starved/replayed: action=%#v starts=%d err=%v", completed, runtime.startCalls, err)
			}
			_, pending, err := fixture.store.ReadManagedEndpointAction(context.Background(), blocked)
			if err != nil {
				t.Fatal(err)
			}
			if cancelFirst {
				if blockedResolutions != 0 || runtime.cancelCalls != 1 || pending.Accepted || pending.Receipt == nil || !pending.Receipt.NotStarted || pending.Receipt.ProcessExited || pending.Receipt.ExitCode != nil {
					t.Fatalf("no-admission cancel depended on start credentials or invented exit: resolutions=%d cancel=%d action=%#v", blockedResolutions, runtime.cancelCalls, pending)
				}
			} else if blockedResolutions != 1 || pending.Accepted || pending.Receipt != nil || pending.ObservationFailure == "" {
				t.Fatalf("dependency failure was hidden, executed or terminalized: resolutions=%d action=%#v", blockedResolutions, pending)
			}
		})
	}
}

func addManagedEndpointFairnessIntent(t *testing.T, fixture managedEndpointOwnerFixture) {
	t.Helper()
	registration := fixture.registration
	registration.Name = "other-private-endpoint"
	endpoint := ManagedEndpoint{Name: registration.Name, URL: registration.URL, Port: registration.Port, SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), State: "stopped", Location: "local", RegisteredBy: fixture.instance.UserID, ServiceDir: t.TempDir()}
	if err := fixture.store.UpsertManagedEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	if _, run, err := fixture.store.ClaimManagedEndpointStart(endpoint.Name, endpoint.RegisteredBy, fixture.instance.RuntimeRoot); err != nil || !run {
		t.Fatalf("second private intent=%t err=%v", run, err)
	}
}

type managedOwnerFairnessRuntime struct{ *managedOwnerRecoveryRuntime }

func (runtime *managedOwnerFairnessRuntime) Start(ctx context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, environment map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	fixture := managedEndpointOwnerFixture{instance: instance}
	runtime.identity = managedOwnerRecoveryIdentity(fixture)
	return runtime.managedOwnerRecoveryRuntime.Start(ctx, instance, action, environment)
}
