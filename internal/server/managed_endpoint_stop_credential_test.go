package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/compute"
	secretstore "x-science/internal/persistence/secrets"
	"x-science/internal/persistence/workspace"
)

func TestManagedEndpointStopAfterDeletedStartCredentialKeepsOwnerAuthority(t *testing.T) {
	t.Run("deleted credential does not strand an admitted owner", func(t *testing.T) {
		s, store, runtime := newManagedEndpointReviewFixture(t)
		registration := compute.ManagedEndpointRegistration{Name: "deleted-start-key", URL: "http://127.0.0.1:25001", Port: 25001,
			CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint", StartScript: "start", StopScript: "stop", LivePath: "/health",
			Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
		if _, err := s.secretStore.Create(secretstore.Secret{ID: registration.CredentialName, UserID: "owner-a", Provider: "nvidia", Value: "controlled-start-key"}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{Name: registration.Name, RegisteredBy: "owner-a",
			URL: registration.URL, Port: registration.Port, CredentialName: &registration.CredentialName, State: "stopped", Location: "local",
			SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath,
			OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), ServiceDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if err := s.ensureManagedInferenceEndpointReady(t.Context(), workspace.KernelFrameAccess{UserID: "owner-a"}, registration.Name); err != nil {
			t.Fatal(err)
		}
		owner, err := store.ReadManagedEndpointLiveOwner(t.Context(), "owner-a", registration.Name)
		if err != nil || runtime.starts != 1 {
			t.Fatalf("normal admitted Start: starts=%d err=%v", runtime.starts, err)
		}
		if deleted, err := s.secretStore.DeleteForUser(registration.CredentialName, "owner-a"); err != nil || !deleted {
			t.Fatalf("delete private startup credential: deleted=%t err=%v", deleted, err)
		}
		if _, err := s.secretStore.Create(secretstore.Secret{ID: "foreign-start-key", Name: registration.CredentialName, UserID: "owner-b", Provider: "nvidia", Value: "foreign-key-must-not-be-used"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.managedEndpointEnvironment(t.Context(), owner, "start"); err == nil {
			t.Fatal("missing startup credential was made optional for Start")
		}
		mux := http.NewServeMux()
		s.registerComputeWorkbenchRoutes(mux)
		response := requestCompute(t, mux, http.MethodPost, "/api/compute/managed-endpoints/"+registration.Name+"/stop", nil, "owner-a")
		if response.Code != http.StatusOK || runtime.starts != 1 || runtime.stops != 1 || runtime.cancels != 0 {
			t.Fatalf("deleted Start credential stranded exact owned Stop: status=%d body=%s starts=%d stops=%d cancels=%d", response.Code, response.Body.String(), runtime.starts, runtime.stops, runtime.cancels)
		}
		values, err := s.managedEndpointEnvironment(t.Context(), owner, "stop")
		if err != nil || values[registration.CredentialName] != "" || values["SERVICE_DIR"] != owner.ServiceDir || values["HOST_PORT"] != "25001" {
			t.Fatalf("Stop did not use the approved credential-free environment: values=%v err=%v", values, err)
		}
		endpoints, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
		if err != nil || len(endpoints) != 1 || endpoints[0].State != "stopped" || endpoints[0].ClaimGeneration != owner.InitialAction.Generation+1 {
			t.Fatalf("Stop did not commit its own stopped projection: endpoints=%+v err=%v", endpoints, err)
		}
		retired, started, err := store.ReadManagedEndpointAction(t.Context(), owner.InitialAction)
		if err != nil || retired.ID != owner.ID || retired.Identity != owner.Identity || started.Receipt == nil || !started.Receipt.Readiness {
			t.Fatalf("Stop replaced original owner/Start evidence: instance=%+v action=%+v err=%v", retired, started, err)
		}
	})
	for _, fault := range []string{"missing store", "unreadable store"} {
		t.Run(fault+" is not positive credential absence", func(t *testing.T) {
			s := &Server{}
			if fault == "unreadable store" {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "secrets"), []byte("private fault fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				s.secretStore = secretstore.New(root)
				if _, err := s.secretStore.ListForUser("owner-a"); err == nil {
					t.Fatal("private credential storage fault was not established")
				}
			}
			instance := compute.ManagedEndpointOwnerInstance{UserID: "owner-a", ServiceDir: t.TempDir(), Registration: compute.ManagedEndpointRegistration{Port: 25001, CredentialName: "NVIDIA_API_KEY"}}
			if _, err := s.managedEndpointEnvironment(context.Background(), instance, "stop"); err == nil {
				t.Fatal("credential storage failure was silently treated as a deleted key")
			}
		})
	}
}
