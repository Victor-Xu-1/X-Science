package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"x-science/internal/compute"
)

type managedOwnerRecoveryRuntime struct {
	store                                 *Store
	identity                              compute.ManagedEndpointRuntimeIdentity
	startCalls, cancelCalls, observeCalls int
	startError                            error
	startHook                             func()
}

func (runtime *managedOwnerRecoveryRuntime) Start(ctx context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, _ map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.startCalls++
	_, err := runtime.store.AcceptManagedEndpointAction(context.Background(), action.Ref, runtime.identity)
	if err != nil {
		return compute.ManagedEndpointOwnerReceipt{}, err
	}
	if runtime.startHook != nil {
		runtime.startHook()
	}
	if runtime.startError != nil {
		return compute.ManagedEndpointOwnerReceipt{}, runtime.startError
	}
	return runtime.Observe(ctx, instance, action)
}
func (runtime *managedOwnerRecoveryRuntime) Stop(context.Context, compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction, map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	return compute.ManagedEndpointOwnerReceipt{}, errors.New("unexpected stop")
}
func (runtime *managedOwnerRecoveryRuntime) Observe(_ context.Context, _ compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.observeCalls++
	code := 0
	return compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: runtime.identity, OwnerVerified: true, Readiness: true}, nil
}
func (runtime *managedOwnerRecoveryRuntime) Cancel(_ context.Context, _ compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, _ map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.cancelCalls++
	return compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, Failure: "explicit task cancellation", Identity: runtime.identity, OwnerVerified: true}, nil
}
func (runtime *managedOwnerRecoveryRuntime) Check(context.Context, compute.ManagedEndpointOwnerInstance) error {
	return nil
}

func newManagedOwnerRecoveryCoordinator(fixture managedEndpointOwnerFixture, runtime *managedOwnerRecoveryRuntime) *compute.ManagedEndpointCoordinator {
	return &compute.ManagedEndpointCoordinator{Store: fixture.store, Runtime: runtime, RuntimeRoot: fixture.instance.RuntimeRoot, Environment: func(context.Context, compute.ManagedEndpointOwnerInstance, string) (map[string]string, error) {
		return map[string]string{"HOST_PORT": "25001", "SERVICE_DIR": fixture.instance.ServiceDir}, nil
	}}
}
func managedOwnerRecoveryIdentity(fixture managedEndpointOwnerFixture) compute.ManagedEndpointRuntimeIdentity {
	return compute.ManagedEndpointRuntimeIdentity{Kind: "native-unit", BootID: "test-boot", Unit: compute.ManagedEndpointUnitName(fixture.instance.ID), InvocationID: strings.Repeat("a", 32), ControlGroup: "/private-recovery-fixture", PID: 42, StartTicks: 12345}
}
func managedOwnerRecoveryClaim(fixture managedEndpointOwnerFixture) compute.ManagedEndpointClaim {
	return compute.ManagedEndpointClaim{UserID: fixture.instance.UserID, Name: fixture.instance.Endpoint, Handle: fixture.action.Ref.ClaimHandle, Generation: fixture.action.Ref.Generation, Registration: fixture.registration, ApprovedHash: fixture.instance.ApprovedHash, ServiceDir: fixture.instance.ServiceDir}
}

func TestManagedEndpointLostAckAndWebReopenObserveOriginalAction(t *testing.T) {
	fixture := newManagedEndpointOwnerFixture(t)
	runtime := &managedOwnerRecoveryRuntime{store: fixture.store, identity: managedOwnerRecoveryIdentity(fixture), startError: errors.New("controlled lost acknowledgement")}
	coordinator := newManagedOwnerRecoveryCoordinator(fixture, runtime)
	if _, _, err := coordinator.Run(context.Background(), managedOwnerRecoveryClaim(fixture)); !errors.Is(err, compute.ErrManagedEndpointOutcomeUnknown) {
		t.Fatalf("lost ack=%v", err)
	}
	_, pending, err := fixture.store.ReadManagedEndpointAction(context.Background(), fixture.action.Ref)
	if err != nil || !pending.Accepted || pending.Receipt != nil || pending.ObservationFailure == "" {
		t.Fatalf("lost ack was terminalized: %#v err=%v", pending, err)
	}
	reopened, err := OpenExisting(fixture.instance.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runtime.store = reopened
	runtime.startError = nil
	coordinator.Store = reopened
	if _, err := coordinator.Reconcile(context.Background(), nil, 8); err != nil {
		t.Fatal(err)
	}
	_, finished, err := reopened.ReadManagedEndpointAction(context.Background(), fixture.action.Ref)
	if err != nil || finished.Receipt == nil || !finished.Receipt.Readiness || runtime.startCalls != 1 || runtime.cancelCalls != 0 {
		t.Fatalf("reopen replayed/killed/lost result: action=%#v start=%d cancel=%d err=%v", finished, runtime.startCalls, runtime.cancelCalls, err)
	}
}

func TestManagedEndpointObserverCancellationAndExplicitCancellationAreSeparate(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "web drain", true: "explicit task stop"}[explicit], func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			ctx, cancel := context.WithCancelCause(context.Background())
			runtime := &managedOwnerRecoveryRuntime{store: fixture.store, identity: managedOwnerRecoveryIdentity(fixture), startHook: func() { cancel(errors.New("controlled context end")) }}
			coordinator := newManagedOwnerRecoveryCoordinator(fixture, runtime)
			coordinator.CancelAuthorized = func(context.Context) bool { return explicit }
			if _, _, err := coordinator.Run(ctx, managedOwnerRecoveryClaim(fixture)); err == nil {
				t.Fatal("context end was hidden")
			}
			_, action, err := fixture.store.ReadManagedEndpointAction(context.Background(), fixture.action.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if action.CancelRequested != explicit || runtime.cancelCalls != map[bool]int{false: 0, true: 1}[explicit] {
				t.Fatalf("cancellation authority leaked: action=%#v calls=%d", action, runtime.cancelCalls)
			}
			if !explicit && action.Receipt != nil {
				t.Fatal("web observer close terminalized original work")
			}
		})
	}
}

func TestManagedEndpointAtomicAdmissionRejectsCancelledOrForeignAction(t *testing.T) {
	fixture := newManagedEndpointOwnerFixture(t)
	identity := managedOwnerRecoveryIdentity(fixture)
	foreign := fixture.action.Ref
	foreign.Generation++
	if _, err := fixture.store.AcceptManagedEndpointAction(context.Background(), foreign, identity); err == nil {
		t.Fatal("foreign generation gained execution admission")
	}
	if won, err := fixture.store.CancelManagedEndpointAction(context.Background(), fixture.action.Ref); err != nil || !won {
		t.Fatalf("cancel=%t err=%v", won, err)
	}
	if accepted, err := fixture.store.AcceptManagedEndpointAction(context.Background(), fixture.action.Ref, identity); err == nil || accepted {
		t.Fatal("cancelled action admitted a script")
	}
	code := 0
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: fixture.action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Readiness: true, OwnerVerified: true, Identity: identity}
	if _, err := fixture.store.FinishManagedEndpointAction(context.Background(), fixture.action.Ref, receipt); err == nil {
		t.Fatal("unaccepted/cancelled action committed live")
	}
}
