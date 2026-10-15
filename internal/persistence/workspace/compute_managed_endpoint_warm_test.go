package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

func TestManagedEndpointWarmStartKeepsPhysicalContainerAndUsesFreshAdmission(t *testing.T) {
	registration := compute.ManagedEndpointRegistration{Name: "warm-fixture", URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint", StartScript: "printf warm", StopScript: "printf stopped", LivePath: "/health", Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "docker", Docker: &compute.ManagedEndpointDockerSpec{Context: "default", Container: "private-warm-fixture"}, Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	fixture := newManagedEndpointOwnerFixture(t, registration)
	worker := managedOwnerRecoveryIdentity(fixture)
	identity := compute.ManagedEndpointRuntimeIdentity{Kind: "docker", DaemonID: "private-daemon", ContainerID: strings.Repeat("c", 64)}
	if accepted, err := fixture.store.AcceptManagedEndpointAction(context.Background(), fixture.action.Ref, worker); err != nil || !accepted {
		t.Fatalf("first admission=%t err=%v", accepted, err)
	}
	code := 0
	ready := compute.ManagedEndpointOwnerReceipt{Ref: fixture.action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Readiness: true}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), fixture.action.Ref, ready); err != nil || state != "live" {
		t.Fatalf("ready=%s err=%v", state, err)
	}
	_, stop := fixture.stop(t)
	stopped := compute.ManagedEndpointOwnerReceipt{Ref: stop.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, StoppedProof: &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), State: "exited", ControlReachable: true}}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), stop.Ref, stopped); err != nil || state != "stopped" {
		t.Fatalf("stopped=%s err=%v", state, err)
	}
	claim, run, err := fixture.store.ClaimManagedEndpointStart(fixture.instance.Endpoint, fixture.instance.UserID)
	if err != nil || !run {
		t.Fatalf("warm claim=%t err=%v", run, err)
	}
	instance, action, err := fixture.store.PrepareManagedEndpointAction(context.Background(), managedEndpointOwnerTestClaim(claim, registration), "must-not-replace-physical-container", fixture.instance.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID != fixture.instance.ID || instance.StartGeneration != fixture.instance.StartGeneration || instance.InitialAction != fixture.instance.InitialAction || instance.Identity != identity || action.Ref.Generation <= stop.Ref.Generation || action.Ref.ClaimHandle == fixture.action.Ref.ClaimHandle || action.Accepted {
		t.Fatalf("warm start rewrote/replayed origin: instance=%#v action=%#v", instance, action)
	}
	worker.InvocationID = strings.Repeat("b", 32)
	worker.PID++
	worker.StartTicks++
	if accepted, err := fixture.store.AcceptManagedEndpointAction(context.Background(), action.Ref, worker); err != nil || !accepted {
		t.Fatalf("fresh warm admission=%t err=%v", accepted, err)
	}
	foreign := ready
	foreign.Ref = action.Ref
	foreign.Identity.ContainerID = strings.Repeat("d", 64)
	if _, err := fixture.store.FinishManagedEndpointAction(context.Background(), action.Ref, foreign); err == nil {
		t.Fatal("foreign same-locator physical container replaced the original owner")
	}
	ready.Ref = action.Ref
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), action.Ref, ready); err != nil || state != "live" {
		t.Fatalf("warm ready=%s err=%v", state, err)
	}
}

func TestManagedEndpointDeleteKeepsUnknownOwnerAndCleansStoppedEvidence(t *testing.T) {
	fixture := newManagedEndpointOwnerFixture(t)
	identity := fixture.ready(t)
	if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err == nil || deleted {
		t.Fatal("live owner was deleted")
	}
	_, action := fixture.stop(t)
	code := 0
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, StoppedProof: &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), action.Ref, receipt); err != nil || state != "stopped" {
		t.Fatalf("stopped=%s err=%v", state, err)
	}
	if deleted, err := fixture.store.DeleteManagedEndpoint(fixture.instance.Endpoint, fixture.instance.UserID); err != nil || !deleted {
		t.Fatalf("stopped delete=%t err=%v", deleted, err)
	}
	for _, table := range []string{"compute_managed_endpoint_actions", "compute_managed_endpoint_instances"} {
		var count int
		if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("owned retired evidence remains: table=%s count=%d err=%v", table, count, err)
		}
	}
}
