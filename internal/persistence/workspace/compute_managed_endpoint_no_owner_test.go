package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

func TestManagedEndpointStopBeforeAdmissionOnlyCleansPrivateObserver(t *testing.T) {
	for _, kind := range []string{"native-unit", "docker"} {
		t.Run(kind, func(t *testing.T) {
			owner := &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: kind, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
			if kind == "docker" {
				owner.Docker = &compute.ManagedEndpointDockerSpec{Context: "default", Container: "potentially-foreign-name"}
			}
			registration := compute.ManagedEndpointRegistration{Name: "no-start", URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint", StartScript: "must not execute", StopScript: "must not execute", LivePath: "/health", Owner: owner}
			fixture := newManagedEndpointOwnerFixture(t, registration)
			claim, run, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot)
			if err != nil || !run {
				t.Fatalf("Stop=%t %v", run, err)
			}
			instance, current, err := fixture.store.CurrentManagedEndpointAction(context.Background(), fixture.instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Ref.Generation != claim.ClaimGeneration || current.Accepted {
				t.Fatal("Stop intent was not atomic/unaccepted")
			}
			if reserved, err := fixture.store.ReserveManagedEndpointNoOwnerCleanup(context.Background(), current.Ref); err != nil || !reserved {
				t.Fatalf("negative proof reservation=%t %v", reserved, err)
			}
			worker := managedOwnerRecoveryIdentity(fixture)
			proof := &compute.ManagedEndpointStoppedProof{Identity: worker, ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}
			receipt := compute.ManagedEndpointOwnerReceipt{Ref: current.Ref, NoScript: true, NoOwnerCreated: true, Stopped: true, WorkerStoppedProof: proof}
			foreign := receipt
			foreignProof := *proof
			foreignProof.Identity.Unit = "foreign.service"
			foreign.WorkerStoppedProof = &foreignProof
			if _, err := fixture.store.FinishManagedEndpointAction(context.Background(), current.Ref, foreign); err == nil {
				t.Fatal("foreign observer proof was accepted")
			}
			if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), current.Ref, receipt); err != nil || state != "stopped" {
				t.Fatalf("negative proof state=%s %v", state, err)
			}
			_, finished, err := fixture.store.ReadManagedEndpointAction(context.Background(), current.Ref)
			if err != nil || finished.Accepted || finished.Receipt == nil || finished.Receipt.ProcessExited || finished.Receipt.ExitCode != nil || finished.Receipt.Identity.Kind != "" {
				t.Fatalf("negative proof invented script/physical identity: %#v %v", finished, err)
			}
			if instance.Identity.Kind != "" || instance.InitialAction != fixture.instance.InitialAction {
				t.Fatal("Stop rewrote the Start origin")
			}
		})
	}
}

func TestManagedEndpointNoOwnerProofRejectsAdoptedOrAcceptedService(t *testing.T) {
	for _, adopt := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted Start", true: "adopted service"}[adopt], func(t *testing.T) {
			var fixture managedEndpointOwnerFixture
			if adopt {
				owner := &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "docker", Docker: &compute.ManagedEndpointDockerSpec{Context: "default", Container: "existing-service"}, Adopt: &compute.ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "known-daemon", ContainerID: strings.Repeat("a", 64)}, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
				fixture = newManagedEndpointOwnerFixture(t, compute.ManagedEndpointRegistration{Name: "adopted", URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint", StartScript: "true", StopScript: "true", LivePath: "/health", Owner: owner})
			} else {
				fixture = newManagedEndpointOwnerFixture(t)
				if _, err := fixture.store.AcceptManagedEndpointAction(context.Background(), fixture.action.Ref, managedOwnerRecoveryIdentity(fixture)); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot); err != nil {
				t.Fatal(err)
			}
			_, current, err := fixture.store.CurrentManagedEndpointAction(context.Background(), fixture.instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reserved, err := fixture.store.ReserveManagedEndpointNoOwnerCleanup(context.Background(), current.Ref); err != nil || reserved {
				t.Fatalf("existing owner became no-owner proof: %t %v", reserved, err)
			}
		})
	}
}
