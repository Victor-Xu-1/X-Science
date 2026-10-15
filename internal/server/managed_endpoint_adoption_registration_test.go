package server

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/compute"
	"x-science/internal/persistence/workspace"
)

func TestManagedEndpointPublicLegacyAdoptionRegistrationIsIdempotent(t *testing.T) {
	s, store, runtime := newManagedEndpointReviewFixture(t)
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "adopt-project", UserID: "owner-a", Name: "Adoption review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "adopt-frame", ProjectID: "adopt-project", AgentName: "OPERON", Status: "processing", ConversationType: "chat"}); err != nil {
		t.Fatal(err)
	}
	seedPermissionTestAgent(t, store, "owner-a")
	permission := func(mode string) {
		t.Helper()
		if _, err := store.SetFrameRuntimeMetadata("adopt-frame", workspace.FrameRuntimeMetadata{FrameID: "adopt-frame", ContextData: map[string]any{
			"web_assistant": map[string]any{"id": "xscience:operon", "conversation_overrides": map[string]any{"permission": mode}},
		}}); err != nil {
			t.Fatal(err)
		}
		if actual, err := s.webSessionApprovalMode("adopt-frame"); err != nil || actual != mode {
			t.Fatalf("controlled conversation permission=%q want=%q %v", actual, mode, err)
		}
	}
	permission("allow")
	access, found, err := store.GetKernelFrameAccessContext(t.Context(), "adopt-frame")
	if err != nil || !found {
		t.Fatalf("controlled registration access: found=%t %v", found, err)
	}
	legacy := compute.ManagedEndpointRegistration{Name: "legacy-adopt", URL: "http://127.0.0.1:25002", Port: 25002,
		CredentialName: "NVIDIA_API_KEY", SkillName: "using-model-endpoint", StartScript: "true", StopScript: "true", LivePath: "/health"}
	credential := legacy.CredentialName
	if err := store.UpsertManagedEndpoint(workspace.ManagedEndpoint{Name: legacy.Name, RegisteredBy: "owner-a", URL: legacy.URL,
		Port: legacy.Port, CredentialName: &credential, State: "live", Location: "local", SkillName: legacy.SkillName,
		StartScript: legacy.StartScript, StopScript: legacy.StopScript, LivePath: legacy.LivePath,
		ApprovedScriptHash: compute.ApprovedManagedEndpointHash(legacy), ServiceDir: filepath.Join(s.fileRoot+"-managed-endpoints", legacy.Name)}); err != nil {
		t.Fatal(err)
	}
	adopt := compute.ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "review-fixture-daemon", ContainerID: strings.Repeat("a", 64)}
	input := cloneManagedEndpointReviewInput(t, map[string]any{
		"name": legacy.Name, "url": legacy.URL, "credential": legacy.CredentialName, "skill": legacy.SkillName,
		"start": legacy.StartScript, "stop": legacy.StopScript, "live": legacy.LivePath,
		"owner": &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "docker", Docker: &compute.ManagedEndpointDockerSpec{Context: "default", Container: legacy.Name},
			Adopt: &adopt, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}},
	})
	if result, err := s.kernelHostRegisterManagedEndpoint(t.Context(), access, cloneManagedEndpointReviewInput(t, input)); err != nil || mapValue(result)["changed"] != true {
		t.Fatalf("first public legacy adoption registration: %+v %v", result, err)
	}
	rows, err := store.ListManagedEndpoints("owner-a", legacy.Name, false)
	if err != nil || len(rows) != 1 || rows[0].OwnerSpec == nil || rows[0].OwnerSpec.LegacyApproval == nil {
		t.Fatalf("server did not bind legacy approval: %+v %v", rows, err)
	}
	bound := rows[0].OwnerSpec.LegacyApproval
	if bound.Registration != legacy || bound.Hash != compute.ApprovedManagedEndpointHash(legacy) {
		t.Fatal("server-generated legacy authority changed original bytes/hash")
	}
	registration := managedEndpointRegistration(rows[0])
	instance, action := managedEndpointReviewStartIntent(t, s, registration)
	if accepted, err := store.AcceptManagedEndpointAction(t.Context(), action.Ref, managedEndpointReviewWorker(instance)); err != nil || !accepted {
		t.Fatalf("controlled adoption observation admission: %t %v", accepted, err)
	}
	if state, err := store.FinishManagedEndpointAction(t.Context(), action.Ref, compute.ManagedEndpointOwnerReceipt{
		Ref: action.Ref, Accepted: true, NoScript: true, Identity: adopt, OwnerVerified: true, Readiness: true,
	}); err != nil || state != "live" {
		t.Fatalf("controlled adoption live receipt: %s %v", state, err)
	}
	before, err := store.ListManagedEndpoints("owner-a", legacy.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.ReadManagedEndpointLiveOwner(t.Context(), "owner-a", legacy.Name)
	if err != nil {
		t.Fatal(err)
	}
	// An identical public call must not ask for a second approval. Conversely,
	// changed or forged requests must not borrow this prior server authority.
	permission("deny")
	if result, err := s.kernelHostRegisterManagedEndpoint(t.Context(), access, cloneManagedEndpointReviewInput(t, input)); err != nil || mapValue(result)["changed"] != false {
		t.Errorf("identical public adoption re-registration lost server-bound approval: %+v %v", result, err)
	}
	for _, change := range []string{"changed_script", "changed_adopt_identity", "forged_legacy_approval"} {
		t.Run(change, func(t *testing.T) {
			request := cloneManagedEndpointReviewInput(t, input)
			submittedOwner := request["owner"].(map[string]any)
			switch change {
			case "changed_script":
				request["start"] = "printf changed"
			case "changed_adopt_identity":
				submittedOwner["adopt"].(map[string]any)["containerId"] = strings.Repeat("b", 64)
			case "forged_legacy_approval":
				submittedOwner["legacyApproval"] = map[string]any{"registration": legacy, "hash": strings.Repeat("f", 64)}
			}
			if result, err := s.kernelHostRegisterManagedEndpoint(t.Context(), access, request); err == nil {
				t.Fatalf("changed/forged request borrowed old approval: %+v", result)
			}
		})
	}
	after, err := store.ListManagedEndpoints("owner-a", legacy.Name, false)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("public re-registration changed existing endpoint authority: %+v %v", after, err)
	}
	retained, err := store.ReadManagedEndpointLiveOwner(t.Context(), "owner-a", legacy.Name)
	if err != nil || !reflect.DeepEqual(owner, retained) || runtime.starts != 0 || runtime.stops != 0 || runtime.cancels != 0 {
		t.Fatalf("public registration changed/dispatched the physical owner: %+v %v", retained, err)
	}
}
