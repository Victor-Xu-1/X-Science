package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

// These controlled receipts test API/Store authority, not real OS ownership.
// Real unit/container evidence is a separate opt-in lifecycle witness.
type managedEndpointAPIRuntime struct {
	store                          *workspace.Store
	starts, stops, cancels, checks int
	onStart                        func()
}

func (runtime *managedEndpointAPIRuntime) identity(instance compute.ManagedEndpointOwnerInstance) compute.ManagedEndpointRuntimeIdentity {
	if instance.Identity.Kind == "native-unit" {
		return instance.Identity
	}
	return compute.ManagedEndpointRuntimeIdentity{Kind: "native-unit", Unit: compute.ManagedEndpointUnitName(instance.ID), BootID: "api-fixture-boot", InvocationID: strings.Repeat("a", 32), ControlGroup: "/private-api-fixture", PID: 42, StartTicks: 12345}
}
func (runtime *managedEndpointAPIRuntime) Start(_ context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, _ map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.starts++
	identity := runtime.identity(instance)
	if _, err := runtime.store.AcceptManagedEndpointAction(context.Background(), action.Ref, identity); err != nil {
		return compute.ManagedEndpointOwnerReceipt{}, err
	}
	if runtime.onStart != nil {
		runtime.onStart()
	}
	code := 0
	return compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Readiness: true}, nil
}
func (runtime *managedEndpointAPIRuntime) Stop(_ context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, _ map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.stops++
	identity := runtime.identity(instance)
	if _, err := runtime.store.AcceptManagedEndpointAction(context.Background(), action.Ref, identity); err != nil {
		return compute.ManagedEndpointOwnerReceipt{}, err
	}
	code := 0
	return compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, StoppedProof: &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}}, nil
}
func (runtime *managedEndpointAPIRuntime) Observe(context.Context, compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction) (compute.ManagedEndpointOwnerReceipt, error) {
	return compute.ManagedEndpointOwnerReceipt{}, compute.ErrManagedEndpointActionPending
}
func (runtime *managedEndpointAPIRuntime) Cancel(_ context.Context, instance compute.ManagedEndpointOwnerInstance, action compute.ManagedEndpointAction, _ map[string]string) (compute.ManagedEndpointOwnerReceipt, error) {
	runtime.cancels++
	if won, err := runtime.store.AcquireManagedEndpointCleanup(context.Background(), action.Ref, nil); err != nil || !won {
		return compute.ManagedEndpointOwnerReceipt{}, errors.Join(err, compute.ErrManagedEndpointStaleAction)
	}
	return compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, Failure: "controlled explicit user cancellation", Identity: runtime.identity(instance), OwnerVerified: true}, nil
}
func (runtime *managedEndpointAPIRuntime) Check(context.Context, compute.ManagedEndpointOwnerInstance) error {
	runtime.checks++
	return nil
}

func managedEndpointAPIFixture(t *testing.T, store *workspace.Store, name string) compute.ManagedEndpointRegistration {
	t.Helper()
	registration := compute.ManagedEndpointRegistration{Name: name, URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint", StartScript: "true", StopScript: "true", LivePath: "/health", Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{Name: name, RegisteredBy: "owner-a", URL: registration.URL, Port: registration.Port, State: "stopped", Location: "local", SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), ServiceDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return registration
}

func TestManagedEndpointHTTPConsumersShareExactOwnerCoordinator(t *testing.T) {
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime := &managedEndpointAPIRuntime{store: store}
	s := New(Options{Workspace: store, FileRoot: t.TempDir(), ManagedEndpointRuntime: runtime})
	registration := managedEndpointAPIFixture(t, store, "api-owner")
	access := workspace.KernelFrameAccess{UserID: "owner-a"}
	if err := s.ensureManagedInferenceEndpointReady(context.Background(), access, registration.Name); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureManagedInferenceEndpointReady(context.Background(), access, registration.Name); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 1 || runtime.checks != 1 {
		t.Fatalf("live owner was restarted/unobserved: starts=%d checks=%d", runtime.starts, runtime.checks)
	}
	mux := http.NewServeMux()
	s.registerComputeWorkbenchRoutes(mux)
	if response := requestCompute(t, mux, http.MethodPost, "/api/compute/managed-endpoints/api-owner/stop", nil, "owner-b"); response.Code != http.StatusNotFound || runtime.stops != 0 {
		t.Fatalf("foreign stop=%d calls=%d", response.Code, runtime.stops)
	}
	requestJSON(t, mux, http.MethodPost, "/api/compute/managed-endpoints/api-owner/stop", nil, "owner-a", http.StatusOK)
	if runtime.stops != 1 {
		t.Fatalf("ordinary stop did not use owner coordinator: %d", runtime.stops)
	}
	managedEndpointAPIFixture(t, store, "api-teardown")
	if err := s.ensureManagedInferenceEndpointReady(context.Background(), access, "api-teardown"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := s.stopManagedEndpointForTeardown(request, "owner-a", "api-teardown"); err != nil {
		t.Fatal(err)
	}
	if runtime.stops != 2 {
		t.Fatalf("teardown bypassed owner coordinator: %d", runtime.stops)
	}
}

func TestManagedEndpointServerTypedUserStopDoesNotIncludeWebDrain(t *testing.T) {
	for _, cause := range []error{ErrGenerationStopped, ErrRuntimeDraining} {
		t.Run(cause.Error(), func(t *testing.T) {
			store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			runtime := &managedEndpointAPIRuntime{store: store, onStart: func() { cancel(cause) }}
			s := New(Options{Workspace: store, FileRoot: t.TempDir(), ManagedEndpointRuntime: runtime})
			registration := managedEndpointAPIFixture(t, store, "api-cancel")
			if err := s.ensureManagedInferenceEndpointReady(ctx, workspace.KernelFrameAccess{UserID: "owner-a"}, registration.Name); err == nil {
				t.Fatal("context end was hidden")
			}
			expected := 0
			if errors.Is(cause, ErrGenerationStopped) {
				expected = 1
			}
			if runtime.cancels != expected {
				t.Fatalf("typed authority cause=%v cancels=%d", cause, runtime.cancels)
			}
		})
	}
}
