package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

// These are controlled Store/API receipts only. No unit, process or container
// is created, and no scientific execution or physical adoption is claimed.
func newManagedEndpointReviewFixture(t *testing.T) (*Server, *workspace.Store, *managedEndpointAPIRuntime) {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.SetComputeBioNeMoSettings(workspace.ComputeBioNeMoSettings{Enabled: true, Mode: "local", HostedHost: "health.api.nvidia.com"}); err != nil {
		t.Fatal(err)
	}
	runtime := &managedEndpointAPIRuntime{store: store}
	return New(Options{Workspace: store, FileRoot: t.TempDir(), ManagedEndpointRuntime: runtime}), store, runtime
}

func managedEndpointReviewWorker(instance compute.ManagedEndpointOwnerInstance) compute.ManagedEndpointRuntimeIdentity {
	return compute.ManagedEndpointRuntimeIdentity{Kind: "native-unit", Unit: compute.ManagedEndpointUnitName(instance.ID), BootID: "review-fixture-boot", InvocationID: strings.Repeat("a", 32), ControlGroup: "/review-fixture", PID: 42, StartTicks: 12345}
}

func managedEndpointReviewStartIntent(t *testing.T, s *Server, registration compute.ManagedEndpointRegistration) (compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction) {
	t.Helper()
	claimed, run, err := s.claimOwnedManagedEndpoint(registration.Name, "owner-a", "start")
	if err != nil || !run {
		t.Fatalf("controlled Start intent: run=%t %v", run, err)
	}
	instance, action, err := s.workspaceStore.PrepareManagedEndpointAction(t.Context(), compute.ManagedEndpointClaim{
		UserID: "owner-a", Name: claimed.Name, Handle: claimed.ClaimHandle, Generation: claimed.ClaimGeneration,
		Registration: registration, ApprovedHash: claimed.ApprovedScriptHash, ServiceDir: claimed.ServiceDir,
	}, "existing-atomic-intent", filepath.Join(s.fileRoot+"-managed-endpoints", ".owners"))
	if err != nil {
		t.Fatal(err)
	}
	return instance, action
}

func seedManagedEndpointReviewNoOwner(t *testing.T, s *Server, kind string) (compute.ManagedEndpointRegistration, compute.ManagedEndpointAction) {
	t.Helper()
	registration := compute.ManagedEndpointRegistration{Name: "never-started", URL: "http://127.0.0.1:25001", Port: 25001,
		SkillName: "using-model-endpoint", StartScript: "must not execute", StopScript: "must not execute", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: kind, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	if kind == "docker" {
		registration.Owner.Docker = &compute.ManagedEndpointDockerSpec{Context: "default", Container: "never-started"}
	}
	if err := s.workspaceStore.UpsertManagedEndpoint(workspace.ManagedEndpoint{Name: registration.Name, RegisteredBy: "owner-a",
		URL: registration.URL, Port: registration.Port, State: "stopped", Location: "local", SkillName: registration.SkillName,
		StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath,
		OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), ServiceDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	instance, _ := managedEndpointReviewStartIntent(t, s, registration)
	if _, run, err := s.claimOwnedManagedEndpoint(registration.Name, "owner-a", "stop"); err != nil || !run {
		t.Fatalf("controlled Stop-before-Accept: run=%t %v", run, err)
	}
	_, action, err := s.workspaceStore.CurrentManagedEndpointAction(t.Context(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := s.workspaceStore.ReserveManagedEndpointNoOwnerCleanup(t.Context(), action.Ref); err != nil || !reserved {
		t.Fatalf("controlled no-owner cleanup reservation: %t %v", reserved, err)
	}
	proof := &compute.ManagedEndpointStoppedProof{Identity: managedEndpointReviewWorker(instance), ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, NoScript: true, NoOwnerCreated: true, Stopped: true, WorkerStoppedProof: proof}
	if state, err := s.workspaceStore.FinishManagedEndpointAction(context.Background(), action.Ref, receipt); err != nil || state != "stopped" {
		t.Fatalf("controlled no-owner finish: %s %v", state, err)
	}
	_, action, err = s.workspaceStore.ReadManagedEndpointAction(t.Context(), action.Ref)
	if err != nil || action.Receipt == nil || action.Receipt.HasPositiveStoppedProof(instance) || action.Receipt.Identity != (compute.ManagedEndpointRuntimeIdentity{}) {
		t.Fatalf("no-owner history invented a physical owner: %+v %v", action, err)
	}
	return registration, action
}

func cloneManagedEndpointReviewInput(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
