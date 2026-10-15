package server

import (
	"net/http"
	"reflect"
	"testing"

	"x-science/internal/persistence/workspace"
)

func TestManagedEndpointNoOwnerHistoryStopAndDisconnectAreIdempotent(t *testing.T) {
	for _, kind := range []string{"native-unit", "docker"} {
		for _, operation := range []string{"repeated_stop", "disconnect"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				s, store, runtime := newManagedEndpointReviewFixture(t)
				registration, original := seedManagedEndpointReviewNoOwner(t, s, kind)
				before, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
				if err != nil || len(before) != 1 || before[0].State != "stopped" {
					t.Fatalf("controlled no-owner endpoint: %+v %v", before, err)
				}
				mux := http.NewServeMux()
				s.registerComputeWorkbenchRoutes(mux)
				if operation == "repeated_stop" {
					for attempt := 0; attempt < 2; attempt++ {
						requestJSON(t, mux, http.MethodPost, "/api/compute/managed-endpoints/"+registration.Name+"/stop", nil, "owner-a", http.StatusOK)
					}
					after, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("no-op Stop replaced a stopped claim/state: %+v %v", after, err)
					}
					_, read, err := store.ReadManagedEndpointAction(t.Context(), original.Ref)
					if err != nil || !reflect.DeepEqual(original, read) {
						t.Fatalf("no-op Stop rewrote negative proof as physical Stop: %+v %v", read, err)
					}
				} else {
					for attempt := 0; attempt < 2; attempt++ {
						response := requestJSON(t, mux, http.MethodPut, "/api/compute/bionemo/enabled", map[string]any{"enabled": false}, "owner-a", http.StatusOK).(map[string]any)
						if response["enabled"] != false || response["incomplete"] == true {
							t.Fatalf("disconnect retained no-owner history: %+v", response)
						}
					}
					if after, err := store.ListManagedEndpoints("owner-a", registration.Name, false); err != nil || len(after) != 0 {
						t.Fatalf("disconnect did not delete resolved negative-proof evidence: %+v %v", after, err)
					}
				}
				if runtime.starts != 0 || runtime.stops != 0 || runtime.cancels != 0 {
					t.Fatalf("no-owner Stop/disconnect dispatched physical work: %+v", runtime)
				}
			})
		}
	}
	for _, status := range []string{"accepted_outcome_unknown", "live"} {
		t.Run(status+"_after_no_owner_history", func(t *testing.T) {
			s, store, runtime := newManagedEndpointReviewFixture(t)
			registration, _ := seedManagedEndpointReviewNoOwner(t, s, "native-unit")
			if status == "live" {
				if err := s.ensureManagedInferenceEndpointReady(t.Context(), workspace.KernelFrameAccess{UserID: "owner-a"}, registration.Name); err != nil {
					t.Fatal(err)
				}
			} else {
				instance, action := managedEndpointReviewStartIntent(t, s, registration)
				if accepted, err := store.AcceptManagedEndpointAction(t.Context(), action.Ref, managedEndpointReviewWorker(instance)); err != nil || !accepted {
					t.Fatalf("controlled unresolved physical owner admission: %t %v", accepted, err)
				}
			}
			mux := http.NewServeMux()
			s.registerComputeWorkbenchRoutes(mux)
			requestJSON(t, mux, http.MethodPost, "/api/compute/managed-endpoints/"+registration.Name+"/stop", nil, "owner-a", http.StatusOK)
			if runtime.stops != 1 {
				t.Fatalf("old no-owner history hid current %s owner: Stop calls=%d", status, runtime.stops)
			}
		})
	}
}
