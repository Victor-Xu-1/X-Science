package server

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	compute "x-science/internal/compute"
	secretstore "x-science/internal/persistence/secrets"
	workspace "x-science/internal/persistence/workspace"
)

func TestComputeBioNeMoSettingsRequireOwnerCredentialAndPersist(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime := &managedEndpointAPIRuntime{store: store}
	server := New(Options{Workspace: store, FileRoot: root, ManagedEndpointRuntime: runtime})
	mux := http.NewServeMux()
	server.registerComputeWorkbenchRoutes(mux)

	defaults := requestJSON(t, mux, http.MethodGet, "/api/compute/bionemo/enabled", nil, "owner-a", http.StatusOK).(map[string]any)
	if defaults["enabled"] != false || defaults["override"] != nil || defaults["mode"] != "local" ||
		defaults["hostedHost"] != "health.api.nvidia.com" {
		t.Fatalf("defaults=%#v", defaults)
	}
	missing := requestCompute(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": true, "mode": "hosted",
	}, "owner-a")
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing credential status=%d body=%s", missing.Code, missing.Body.String())
	}
	if _, err := server.secretStore.Create(secretstore.Secret{
		ID: "NVIDIA_API_KEY", UserID: "owner-a", Provider: "nvidia", Value: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	enabled := requestJSON(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": true, "mode": "hosted", "hostedHost": "https://API.NVIDIA.EXAMPLE/",
	}, "owner-a", http.StatusOK).(map[string]any)
	if enabled["enabled"] != true || enabled["mode"] != "hosted" ||
		enabled["hostedHost"] != "api.nvidia.example" {
		t.Fatalf("enabled=%#v", enabled)
	}
	persisted := requestJSON(t, mux, http.MethodGet, "/api/compute/bionemo/enabled", nil, "owner-a", http.StatusOK).(map[string]any)
	if persisted["enabled"] != true || persisted["hostedHost"] != "api.nvidia.example" {
		t.Fatalf("persisted=%#v", persisted)
	}
	foreign := requestCompute(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": true, "mode": "local",
	}, "owner-b")
	if foreign.Code != http.StatusBadRequest {
		t.Fatalf("foreign credential status=%d body=%s", foreign.Code, foreign.Body.String())
	}
	invalidMode := requestCompute(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": false, "mode": "remote",
	}, "owner-a")
	if invalidMode.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d body=%s", invalidMode.Code, invalidMode.Body.String())
	}
	invalidHost := requestCompute(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": false, "hostedHost": "http://api.nvidia.example",
	}, "owner-a")
	if invalidHost.Code != http.StatusBadRequest {
		t.Fatalf("invalid host status=%d body=%s", invalidHost.Code, invalidHost.Body.String())
	}
	registration := compute.ManagedEndpointRegistration{
		Name: "bionemo-fixture", URL: "http://127.0.0.1:9444", Port: 9444,
		CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint",
		StartScript: "start", StopScript: "stop", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}},
	}
	if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{
		Name: registration.Name, RegisteredBy: "owner-a", URL: registration.URL, Port: registration.Port,
		State: "stopped", Location: "local", SkillName: registration.SkillName, CredentialName: &registration.CredentialName,
		LivePath: registration.LivePath, StartScript: registration.StartScript, StopScript: registration.StopScript,
		ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), OwnerSpec: registration.Owner, ServiceDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	// Establish actual Store admission and a matching controlled owner receipt;
	// adding a typed descriptor to a bare live label is not owned liveness.
	if err := server.ensureManagedInferenceEndpointReady(t.Context(), workspace.KernelFrameAccess{UserID: "owner-a"}, registration.Name); err != nil {
		t.Fatal(err)
	}
	liveOwner, err := store.ReadManagedEndpointLiveOwner(t.Context(), "owner-a", registration.Name)
	if err != nil || runtime.starts != 1 || liveOwner.Identity.Kind != "native-unit" {
		t.Fatalf("owned Start did not establish live receipt: %+v starts=%d err=%v", liveOwner, runtime.starts, err)
	}
	_, started, err := store.ReadManagedEndpointAction(t.Context(), liveOwner.InitialAction)
	if err != nil || !started.Accepted || started.Receipt == nil || started.Receipt.ValidateFor(liveOwner, liveOwner.InitialAction) != nil {
		t.Fatalf("owned Start has no matching durable receipt: %+v %v", started, err)
	}
	disabled := requestJSON(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": false, "mode": "local",
	}, "owner-a", http.StatusOK).(map[string]any)
	if disabled["enabled"] != false || disabled["teardown"] == nil {
		t.Fatalf("disabled=%#v", disabled)
	}
	teardown := disabled["teardown"].(map[string]any)
	removed := teardown["removed"].([]any)
	if len(removed) != 1 || removed[0] != "bionemo-fixture" || len(teardown["failed"].([]any)) != 0 {
		t.Fatalf("teardown=%#v", teardown)
	}
	if runtime.starts != 1 || runtime.stops != 1 || runtime.cancels != 0 {
		t.Fatalf("BioNeMo disable did not settle one controlled owned Stop: starts=%d stops=%d cancels=%d", runtime.starts, runtime.stops, runtime.cancels)
	}
	// The existing controlled Stop commits admission and positive owner proof;
	// deletion cannot succeed while this FK-protected instance is unresolved.
	if _, _, err := store.ReadManagedEndpointAction(t.Context(), liveOwner.InitialAction); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired owner evidence was not removed with its registration: %v", err)
	}
	if endpoints, err := store.ListManagedEndpoints("owner-a", "", false); err != nil || len(endpoints) != 0 {
		t.Fatalf("endpoints after teardown=%#v err=%v", endpoints, err)
	}
	method := requestCompute(t, mux, http.MethodPost, "/api/compute/bionemo/enabled", nil, "owner-a")
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d body=%s", method.Code, method.Body.String())
	}
}

func TestComputeBioNeMoDisableKeepsConsentWhenTeardownFails(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.SetComputeBioNeMoSettings(workspace.ComputeBioNeMoSettings{
		Enabled: true, Mode: "local", HostedHost: compute.DefaultBioNeMoHostedHost,
	}); err != nil {
		t.Fatal(err)
	}
	registration := compute.ManagedEndpointRegistration{
		Name: "drifted-fixture", URL: "http://127.0.0.1:9555", Port: 9555,
		CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint",
		StartScript: "start", StopScript: "exit 0", LivePath: "/health",
		Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}},
	}
	if err := registration.Owner.Validate(); err != nil {
		t.Fatalf("approval-drift fixture has an invalid owner contract: %v", err)
	}
	now := time.Now().UTC()
	if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{
		Name: registration.Name, RegisteredBy: "owner-a", URL: registration.URL, Port: registration.Port,
		State: "live", SkillName: registration.SkillName, CredentialName: &registration.CredentialName,
		LivePath: registration.LivePath, StartScript: registration.StartScript, StopScript: registration.StopScript,
		ApprovedScriptHash: "approval-drift", StateChangedAt: &now, OwnerSpec: registration.Owner, ServiceDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
	if err != nil || len(before) != 1 {
		t.Fatalf("approval-drift fixture missing: %+v %v", before, err)
	}
	runtime := &managedEndpointAPIRuntime{store: store}
	server := New(Options{Workspace: store, FileRoot: root, ManagedEndpointRuntime: runtime})
	mux := http.NewServeMux()
	server.registerComputeWorkbenchRoutes(mux)

	response := requestJSON(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{
		"enabled": false,
	}, "owner-a", http.StatusOK).(map[string]any)
	if response["enabled"] != true || response["incomplete"] != true {
		t.Fatalf("response=%#v", response)
	}
	teardown := response["teardown"].(map[string]any)
	failed := teardown["failed"].([]any)
	if len(teardown["removed"].([]any)) != 0 || len(failed) != 1 ||
		failed[0].(map[string]any)["name"] != registration.Name || !strings.Contains(stringValue(failed[0].(map[string]any)["error"]), "approved hash") {
		t.Fatalf("teardown did not stop specifically at the approval-hash fence: %+v", teardown)
	}
	settings, err := store.GetComputeBioNeMoSettings()
	if err != nil || !settings.Enabled {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	endpoints, err := store.ListManagedEndpoints("owner-a", "drifted-fixture", false)
	if err != nil || !reflect.DeepEqual(before, endpoints) {
		t.Fatalf("endpoints=%#v err=%v", endpoints, err)
	}
	if pending, err := store.PendingManagedEndpointActions(t.Context(), nil, 16); err != nil || len(pending) != 0 ||
		runtime.starts != 0 || runtime.stops != 0 || runtime.cancels != 0 || runtime.checks != 0 {
		t.Fatalf("hash refusal created a claim/executed lifecycle work: pending=%+v runtime=%+v err=%v", pending, runtime, err)
	}
}
