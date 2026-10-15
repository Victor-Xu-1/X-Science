package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/compute"
)

type managedEndpointOwnerFixture struct {
	store        *Store
	registration compute.ManagedEndpointRegistration
	instance     compute.ManagedEndpointOwnerInstance
	action       compute.ManagedEndpointAction
}

func newManagedEndpointOwnerFixture(t *testing.T, overrides ...compute.ManagedEndpointRegistration) managedEndpointOwnerFixture {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Exercise the proposed additive contract in a private database while its
	// migration number remains controller-owned. Once registered, Open supplies
	// these objects and this branch does not reapply or edit the migration.
	rows, err := store.db.Query(`SELECT owner_spec_json FROM compute_managed_endpoints LIMIT 0`)
	if err == nil {
		_ = rows.Close()
	} else {
		if !strings.Contains(err.Error(), "no such column") {
			t.Fatal(err)
		}
		for _, statement := range managedEndpointOwnerSchemaStatements {
			if _, err := store.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	registration := compute.ManagedEndpointRegistration{Name: "owner-fixture", URL: "http://127.0.0.1:25001", Port: 25001, SkillName: "using-model-endpoint", StartScript: "printf started", StopScript: "printf stopped", LivePath: "/health", Owner: &compute.ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit", Output: compute.ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}}}
	if len(overrides) > 0 {
		registration = overrides[0]
	}
	endpoint := ManagedEndpoint{Name: registration.Name, URL: registration.URL, Port: registration.Port, SkillName: registration.SkillName, StartScript: registration.StartScript, StopScript: registration.StopScript, LivePath: registration.LivePath, OwnerSpec: registration.Owner, ApprovedScriptHash: compute.ApprovedManagedEndpointHash(registration), State: "stopped", Location: "local", RegisteredBy: "owner-a", ServiceDir: t.TempDir()}
	if err := store.UpsertManagedEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := t.TempDir()
	claimed, run, err := store.ClaimManagedEndpointStart(endpoint.Name, endpoint.RegisteredBy, runtimeRoot)
	if err != nil || !run {
		t.Fatalf("claim run=%t err=%v", run, err)
	}
	instance, action, err := store.PrepareManagedEndpointAction(context.Background(), managedEndpointOwnerTestClaim(claimed, registration), fmt.Sprintf("private-owner-%x", time.Now().UnixNano()), runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	return managedEndpointOwnerFixture{store: store, registration: registration, instance: instance, action: action}
}

func managedEndpointOwnerTestClaim(endpoint ManagedEndpoint, registration compute.ManagedEndpointRegistration) compute.ManagedEndpointClaim {
	return compute.ManagedEndpointClaim{UserID: endpoint.RegisteredBy, Name: endpoint.Name, Handle: endpoint.ClaimHandle, Generation: endpoint.ClaimGeneration, Registration: registration, ApprovedHash: endpoint.ApprovedScriptHash, ServiceDir: endpoint.ServiceDir}
}

func (fixture managedEndpointOwnerFixture) ready(t *testing.T) compute.ManagedEndpointRuntimeIdentity {
	t.Helper()
	identity := compute.ManagedEndpointRuntimeIdentity{Kind: "native-unit", BootID: "test-boot", Unit: compute.ManagedEndpointUnitName(fixture.instance.ID), InvocationID: strings.Repeat("a", 32), ControlGroup: "/private-owner-fixture", PID: 42, StartTicks: 12345}
	if accepted, err := fixture.store.AcceptManagedEndpointAction(context.Background(), fixture.action.Ref, identity); err != nil || !accepted {
		t.Fatalf("admission=%t err=%v", accepted, err)
	}
	code := 0
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: fixture.action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Readiness: true, Transcript: "started"}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), fixture.action.Ref, receipt); err != nil || state != "live" {
		t.Fatalf("start state=%q err=%v", state, err)
	}
	return identity
}

func (fixture managedEndpointOwnerFixture) stop(t *testing.T) (compute.ManagedEndpointOwnerInstance, compute.ManagedEndpointAction) {
	t.Helper()
	claimed, run, err := fixture.store.ClaimManagedEndpointStop(fixture.instance.Endpoint, fixture.instance.UserID, fixture.instance.RuntimeRoot)
	if err != nil || !run {
		t.Fatalf("stop claim run=%t err=%v", run, err)
	}
	instance, action, err := fixture.store.PrepareManagedEndpointAction(context.Background(), managedEndpointOwnerTestClaim(claimed, fixture.registration), "must-not-replace-owner", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worker := instance.WorkerIdentity
	if worker == (compute.ManagedEndpointRuntimeIdentity{}) {
		worker = instance.Identity
	}
	if accepted, err := fixture.store.AcceptManagedEndpointAction(context.Background(), action.Ref, worker); err != nil || !accepted {
		t.Fatalf("stop admission=%t err=%v", accepted, err)
	}
	return instance, action
}

func TestManagedEndpointFailedStopPreservesPhysicalOwnerAcrossClaimGenerations(t *testing.T) {
	fixture := newManagedEndpointOwnerFixture(t)
	identity := fixture.ready(t)
	_, first := fixture.stop(t)
	code := 1
	failure := compute.ManagedEndpointOwnerReceipt{Ref: first.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Failure: "private stop failed", Transcript: "failed stop"}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), first.Ref, failure); err != nil || state != "failed" {
		t.Fatalf("failed stop state=%q err=%v", state, err)
	}
	instance, second := fixture.stop(t)
	if instance.ID != fixture.instance.ID || instance.Identity != identity || second.Ref.Generation <= first.Ref.Generation {
		t.Fatalf("retry changed owner: instance=%#v first=%#v second=%#v", instance, first.Ref, second.Ref)
	}
	if _, err := fixture.store.FinishManagedEndpointStop(second.Ref.ClaimHandle, "untyped bypass", nil); err == nil {
		t.Fatal("legacy finisher bypassed owner proof")
	}
	code = 0
	proof := &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), ControlReachable: true, State: "inactive", CgroupEmpty: true}
	receipt := compute.ManagedEndpointOwnerReceipt{Ref: second.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, StoppedProof: proof}
	if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), second.Ref, receipt); err != nil || state != "stopped" {
		t.Fatalf("retry state=%q err=%v", state, err)
	}
}

func TestManagedEndpointOwnerRejectsForeignReceiptAndCorruptStoredIdentity(t *testing.T) {
	t.Run("foreign physical incarnation", func(t *testing.T) {
		fixture := newManagedEndpointOwnerFixture(t)
		identity := fixture.ready(t)
		_, action := fixture.stop(t)
		identity.InvocationID = strings.Repeat("b", 32)
		code := 0
		receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, StoppedProof: &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}}
		if _, err := fixture.store.FinishManagedEndpointAction(context.Background(), action.Ref, receipt); err == nil {
			t.Fatal("foreign physical owner was retired")
		}
		if _, err := fixture.store.CancelManagedEndpointAction(context.Background(), action.Ref); err != nil {
			t.Fatalf("foreign receipt changed current claim: %v", err)
		}
	})
	for _, field := range []string{"id", "userId", "endpoint", "approvedHash", "startGeneration"} {
		t.Run("corrupt "+field, func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			encoded, err := json.Marshal(fixture.instance)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			value[field] = "foreign"
			if field == "startGeneration" {
				value[field] = float64(999)
			}
			encoded, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.db.Exec(`UPDATE compute_managed_endpoint_instances SET instance_json=? WHERE instance_id=?`, string(encoded), fixture.instance.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := fixture.store.ReadManagedEndpointAction(context.Background(), fixture.action.Ref); !errors.Is(err, compute.ErrManagedEndpointCorruptOwner) {
				t.Fatalf("corruption read error=%v", err)
			}
			if _, err := fixture.store.CancelManagedEndpointAction(context.Background(), fixture.action.Ref); !errors.Is(err, compute.ErrManagedEndpointCorruptOwner) {
				t.Fatalf("corruption cancel error=%v", err)
			}
		})
	}
}

func TestManagedEndpointFailedCleanupNeedsPositiveStoppedProofToRetire(t *testing.T) {
	for _, positive := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[positive], func(t *testing.T) {
			fixture := newManagedEndpointOwnerFixture(t)
			identity := fixture.ready(t)
			_, action := fixture.stop(t)
			code := -1
			receipt := compute.ManagedEndpointOwnerReceipt{Ref: action.Ref, Accepted: true, ProcessExited: true, ExitCode: &code, Identity: identity, OwnerVerified: true, Stopped: true, Failure: "cancelled private cleanup"}
			if positive {
				receipt.StoppedProof = &compute.ManagedEndpointStoppedProof{Identity: identity, ObservedAt: time.Now().UTC(), State: "inactive", ControlReachable: true, CgroupEmpty: true}
			}
			if state, err := fixture.store.FinishManagedEndpointAction(context.Background(), action.Ref, receipt); err != nil || state != "failed" {
				t.Fatalf("state=%q err=%v", state, err)
			}
			var active int
			if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM compute_managed_endpoint_instances WHERE instance_id=? AND retired_at IS NULL`, fixture.instance.ID).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if (active == 0) != positive {
				t.Fatalf("positive=%t active=%d", positive, active)
			}
		})
	}
}

func TestManagedEndpointCancelAndSameHashRegistrationCannotRevokeCommittedLiveOwner(t *testing.T) {
	fixture := newManagedEndpointOwnerFixture(t)
	identity := fixture.ready(t)
	if cancelled, err := fixture.store.CancelManagedEndpointAction(context.Background(), fixture.action.Ref); err != nil || cancelled {
		t.Fatalf("late cancellation=%t err=%v", cancelled, err)
	}
	endpoint := ManagedEndpoint{Name: fixture.registration.Name, RegisteredBy: fixture.instance.UserID, ApprovedScriptHash: fixture.instance.ApprovedHash, State: "stopped"}
	if err := fixture.store.UpsertManagedEndpoint(endpoint); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.ListManagedEndpoints(fixture.instance.UserID, fixture.instance.Endpoint, false)
	if err != nil || len(stored) != 1 || stored[0].State != "live" || stored[0].URL != fixture.registration.URL {
		t.Fatalf("same hash reset live owner: %#v %v", stored, err)
	}
	_, action := fixture.stop(t)
	instance, _, err := fixture.store.ReadManagedEndpointAction(context.Background(), action.Ref)
	if err != nil || instance.Identity != identity || instance.ID != fixture.instance.ID {
		t.Fatalf("physical owner changed: %#v %v", instance, err)
	}
	page, err := fixture.store.PendingManagedEndpointActions(context.Background(), nil, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("first page=%#v err=%v", page, err)
	}
	page, err = fixture.store.PendingManagedEndpointActions(context.Background(), &page[0], 1)
	if err != nil || len(page) != 0 {
		t.Fatalf("next page=%#v err=%v", page, err)
	}
}
