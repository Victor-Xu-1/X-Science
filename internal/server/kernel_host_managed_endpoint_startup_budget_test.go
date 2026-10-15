package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

func TestKernelHostManagedEndpointRegistrationPreservesApprovedStartupBudget(t *testing.T) {
	ctx := context.Background()
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	if _, err := store.SetComputeBioNeMoSettings(workspace.ComputeBioNeMoSettings{
		Enabled: true, Mode: "local", HostedHost: "https://health.api.nvidia.com",
	}); err != nil {
		t.Fatal(err)
	}
	registration := compute.ManagedEndpointRegistration{
		Name: "startup-policy", URL: "http://127.0.0.1:24567", Port: 24567,
		CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint",
		StartScript: ":", StopScript: ":", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{
			Version: 1, Kind: "native-unit", StartupTimeoutSeconds: 300,
			Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2},
		},
	}
	approved := compute.ApprovedManagedEndpointHash(registration)
	if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{
		Name: registration.Name, URL: registration.URL, Port: registration.Port,
		State: "stopped", Location: "local", SkillName: registration.SkillName,
		CredentialName: &registration.CredentialName, LivePath: registration.LivePath,
		StartScript: registration.StartScript, StopScript: registration.StopScript,
		OwnerSpec: registration.Owner, ApprovedScriptHash: approved,
		ServiceDir: t.TempDir(), RegisteredBy: identity.access.UserID,
	}); err != nil {
		t.Fatal(err)
	}
	var owner map[string]any
	if err := json.Unmarshal([]byte(`{"version":1,"kind":"native-unit","output":{"segmentBytes":4096,"segments":2},"startupTimeoutSeconds":300}`), &owner); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"name": registration.Name, "url": registration.URL, "skill": registration.SkillName,
		"credential": registration.CredentialName, "start": registration.StartScript,
		"stop": registration.StopScript, "live": registration.LivePath, "owner": owner,
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := app.handleKernelMiscHostCall(ctx, identity.access, "host.model_endpoints.register", []any{input}, nil)
		if err != nil || mapValue(result)["registered"] != true || mapValue(result)["changed"] != false {
			t.Fatalf("approved registration attempt %d: result=%#v err=%v", attempt, result, err)
		}
	}
	endpoints, err := store.ListManagedEndpoints(identity.access.UserID, registration.Name, false)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("registered endpoints=%#v err=%v", endpoints, err)
	}
	endpoint := endpoints[0]
	if endpoint.OwnerSpec == nil || endpoint.OwnerSpec.StartupTimeoutSeconds != 300 ||
		endpoint.ApprovedScriptHash != approved || compute.ApprovedManagedEndpointHash(managedEndpointRegistration(endpoint)) != approved ||
		endpoint.State != "stopped" || endpoint.ClaimToken != "" || endpoint.ClaimGeneration != 0 {
		t.Fatalf("host registration changed the approved startup policy or started it: %#v", endpoint)
	}
	if refs, err := store.PendingManagedEndpointActions(ctx, nil, 1); err != nil || len(refs) != 0 {
		t.Fatalf("registration created an action: refs=%#v err=%v", refs, err)
	}
	if instance, err := store.ReadManagedEndpointLiveOwner(ctx, identity.access.UserID, registration.Name); instance.ID != "" || !errors.Is(err, compute.ErrManagedEndpointOwnershipUnknown) {
		t.Fatalf("registration created an owner: instance=%#v err=%v", instance, err)
	}
}
