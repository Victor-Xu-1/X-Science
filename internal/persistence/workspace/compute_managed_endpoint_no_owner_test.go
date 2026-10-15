package workspace

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

func TestManagedEndpointCancelledNoOwnerStopRetryPreservesOriginalReceipt(t *testing.T) {
	seedCancelled := func(t *testing.T, kind string) (managedEndpointOwnerFixture, compute.ManagedEndpointAction) {
		t.Helper()
		owner := &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: kind, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}
		if kind == "docker" {
			owner.Docker = &compute.ManagedEndpointDockerSpec{Context: "default", Container: "must-not-touch-a-container"}
		}
		registration := compute.ManagedEndpointRegistration{Name: "cancelled-no-owner", URL: "http://127.0.0.1:25001", Port: 25001,
			SkillName: "using-model-endpoint", StartScript: "must not execute", StopScript: "must not execute", LivePath: "/health", Owner: owner}
		fixture := newManagedEndpointOwnerFixture(t, registration)
		if _, run, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot); err != nil || !run {
			t.Fatalf("Stop-before-admission: run=%t err=%v", run, err)
		}
		_, action, err := fixture.store.CurrentManagedEndpointAction(t.Context(), fixture.instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if won, err := fixture.store.CancelManagedEndpointAction(t.Context(), action.Ref); err != nil || !won {
			t.Fatalf("explicit Stop cancellation: won=%t err=%v", won, err)
		}
		if reserved, err := fixture.store.ReserveManagedEndpointNoOwnerCleanup(t.Context(), action.Ref); err != nil || !reserved {
			t.Fatalf("negative proof reservation: reserved=%t err=%v", reserved, err)
		}
		receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, NoScript: true, NoOwnerCreated: true, Stopped: true,
			Failure: "controlled explicit Stop cancellation", WorkerStoppedProof: &compute.ManagedEndpointStoppedProof{
				Identity: managedOwnerRecoveryIdentity(fixture), ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}}
		if state, err := fixture.store.FinishManagedEndpointAction(t.Context(), action.Ref, receipt); err != nil || state != "failed" {
			t.Fatalf("cancelled Stop did not retain failed result: state=%s err=%v", state, err)
		}
		_, finished, err := fixture.store.ReadManagedEndpointAction(t.Context(), action.Ref)
		if err != nil || !finished.CancelRequested || finished.Accepted || finished.Receipt == nil || finished.Receipt.HasPositiveStoppedProof(fixture.instance) {
			t.Fatalf("cancelled negative receipt invented an admitted physical owner: action=%+v err=%v", finished, err)
		}
		return fixture, finished
	}
	for _, kind := range []string{"native-unit", "docker"} {
		t.Run(kind+" explicit retry settles projection only", func(t *testing.T) {
			fixture, failed := seedCancelled(t, kind)
			for retry := 0; retry < 2; retry++ {
				endpoint, run, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot)
				if err != nil || run || endpoint.State != "stopped" {
					t.Fatalf("retry %d stranded a proven never-created owner: state=%s run=%t err=%v", retry, endpoint.State, run, err)
				}
				_, after, err := fixture.store.ReadManagedEndpointAction(t.Context(), failed.Ref)
				if err != nil || !reflect.DeepEqual(after, failed) {
					t.Fatalf("retry rewrote the cancelled action/receipt: before=%+v after=%+v err=%v", failed, after, err)
				}
			}
			var accepted, actions int
			if err := fixture.store.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(accepted),0) FROM compute_managed_endpoint_actions WHERE instance_id=?`, fixture.instance.ID).Scan(&actions, &accepted); err != nil || actions != 2 || accepted != 0 {
				t.Fatalf("retry created/admitted new execution: actions=%d accepted=%d err=%v", actions, accepted, err)
			}
		})
	}
	t.Run("failed without history is not a negative proof", func(t *testing.T) {
		store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		registration := compute.ManagedEndpointRegistration{Name: "unknown-failed", URL: "http://127.0.0.1:25001", Port: 25001,
			SkillName: "using-model-endpoint", StartScript: "true", StopScript: "true", LivePath: "/health",
			Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
		if err := store.UpsertManagedEndpoint(ManagedEndpoint{Name: registration.Name, RegisteredBy: "owner-a", State: "failed",
			URL: registration.URL, Port: registration.Port, SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript,
			LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), ServiceDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		before, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, run, err := store.ClaimManagedEndpointStop(registration.Name, "owner-a", t.TempDir()); err == nil || run {
			t.Fatalf("missing history was treated as proven no-owner: run=%t err=%v", run, err)
		}
		after, err := store.ListManagedEndpoints("owner-a", registration.Name, false)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("failed unknown endpoint changed: before=%+v after=%+v err=%v", before, after, err)
		}
	})
	t.Run("live successor keeps its own Stop claim", func(t *testing.T) {
		fixture, failed := seedCancelled(t, "native-unit")
		registration := fixture.registration
		if err := fixture.store.UpsertManagedEndpoint(ManagedEndpoint{Name: registration.Name, RegisteredBy: fixture.instance.UserID, State: "stopped", Location: "local",
			URL: registration.URL, Port: registration.Port, SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript,
			LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: fixture.instance.ApprovedHash, ServiceDir: fixture.instance.ServiceDir}); err != nil {
			t.Fatal(err)
		}
		claimed, run, err := fixture.store.ClaimManagedEndpointStart(registration.Name, fixture.instance.UserID, fixture.instance.RuntimeRoot)
		if err != nil || !run {
			t.Fatalf("fresh successor Start: run=%t err=%v", run, err)
		}
		fixture.instance, fixture.action, err = fixture.store.PrepareManagedEndpointAction(t.Context(), managedEndpointOwnerTestClaim(claimed, registration), "existing-successor-intent", fixture.instance.RuntimeRoot)
		if err != nil || fixture.instance.ID == failed.Ref.InstanceID {
			t.Fatalf("fresh successor reused retired no-owner instance: instance=%+v err=%v", fixture.instance, err)
		}
		fixture.ready(t)
		if _, run, err := fixture.store.ClaimManagedEndpointStop(registration.Name, fixture.instance.UserID, fixture.instance.RuntimeRoot); err != nil || !run {
			t.Fatalf("old no-owner proof suppressed live successor Stop: run=%t err=%v", run, err)
		}
		current, action, err := fixture.store.CurrentManagedEndpointAction(t.Context(), fixture.instance.ID)
		if err != nil || current.ID != fixture.instance.ID || action.Ref.Kind != "stop" || action.Ref.InstanceID == failed.Ref.InstanceID {
			t.Fatalf("Stop lost successor authority: instance=%+v action=%+v err=%v", current, action, err)
		}
	})
}

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
