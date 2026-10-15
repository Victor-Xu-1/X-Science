package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"x-science/internal/compute"
)

func TestManagedEndpointStartupBudgetPersistsAndFencesApprovalDrift(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	initial, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = initial.Close() })
	registration := compute.ManagedEndpointRegistration{
		Name: "startup-policy", URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint",
		StartScript: ":", StopScript: ":", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{
			Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2},
			StartupTimeoutSeconds: 300,
		},
	}
	approved := compute.ApprovedManagedEndpointHash(registration)
	endpoint := ManagedEndpoint{Name: registration.Name, URL: registration.URL, Port: registration.Port,
		SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript,
		LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: approved,
		State: "stopped", Location: "local", RegisteredBy: "startup-owner", ServiceDir: t.TempDir()}
	if err := initial.UpsertManagedEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	claimed, run, err := initial.ClaimManagedEndpointStart(endpoint.Name, endpoint.RegisteredBy, root)
	if err != nil || !run {
		t.Fatalf("claim run=%t err=%v", run, err)
	}
	instance, action, err := initial.PrepareManagedEndpointAction(ctx, managedEndpointOwnerTestClaim(claimed, registration), "unused-existing-intent", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	endpoints, err := reopened.ListManagedEndpoints(endpoint.RegisteredBy, endpoint.Name, false)
	if err != nil || len(endpoints) != 1 || endpoints[0].OwnerSpec == nil || endpoints[0].OwnerSpec.StartupTimeoutSeconds != 300 || endpoints[0].ApprovedScriptHash != approved {
		t.Fatalf("reopened registration=%#v err=%v", endpoints, err)
	}
	stored, pending, err := reopened.ReadManagedEndpointAction(ctx, action.Ref)
	if err != nil || stored.ID != instance.ID || stored.Registration.Owner.StartupTimeoutSeconds != 300 || stored.ApprovedHash != approved || pending.Ref != action.Ref || pending.Accepted || pending.Receipt != nil {
		t.Fatalf("reopened instance=%#v action=%#v err=%v", stored, pending, err)
	}
	changed := registration
	changedOwner := *registration.Owner
	changedOwner.StartupTimeoutSeconds = 600
	changed.Owner = &changedOwner
	if compute.ApprovedManagedEndpointHash(changed) == approved {
		t.Fatal("changed startup policy reused the original approval hash")
	}
	if _, _, err := reopened.PrepareManagedEndpointAction(ctx, managedEndpointOwnerTestClaim(claimed, changed), "must-not-create-owner", root); !errors.Is(err, compute.ErrManagedEndpointStaleAction) {
		t.Fatalf("changed budget borrowed original approval: %v", err)
	}
	stored, pending, err = reopened.ReadManagedEndpointAction(ctx, action.Ref)
	if err != nil || stored.ID != instance.ID || stored.Registration.Owner.StartupTimeoutSeconds != 300 || stored.ApprovedHash != approved || pending.Ref != action.Ref || pending.Accepted || pending.CancelRequested || pending.Receipt != nil {
		t.Fatalf("approval rejection changed original intent: instance=%#v action=%#v err=%v", stored, pending, err)
	}
	refs, err := reopened.PendingManagedEndpointActions(ctx, nil, 2)
	if err != nil || len(refs) != 1 || refs[0] != action.Ref {
		t.Fatalf("approval rejection created another action: refs=%#v err=%v", refs, err)
	}
}
