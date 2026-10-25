package toolprogress

import (
	"context"
	"testing"
	"time"
)

func admissionSnapshotForTest() *ResourceAdmissionSnapshot {
	return &ResourceAdmissionSnapshot{DecisionAt: "2026-01-02T03:04:05Z", Reason: ResourceAdmissionCapacity,
		CapacityAvailableBytes: 900, UnusedPromisedBytes: 600, NetAvailableBytes: 300,
		RequestedBytes: 400, RequiredBytes: 400}
}

func TestResourceAdmissionSnapshotRejectsInvalidPublicFacts(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(*ResourceAdmissionSnapshot)
	}{
		{"unbounded reason", func(s *ResourceAdmissionSnapshot) { s.Reason = "private/path/or/process" }},
		{"bad time", func(s *ResourceAdmissionSnapshot) { s.DecisionAt = "private-value" }},
		{"negative", func(s *ResourceAdmissionSnapshot) { s.UnusedPromisedBytes = -1 }},
		{"unsafe JSON integer", func(s *ResourceAdmissionSnapshot) { s.CapacityAvailableBytes = maxAdmissionPublicInteger + 1 }},
		{"inconsistent net", func(s *ResourceAdmissionSnapshot) { s.NetAvailableBytes = 400 }},
		{"no demand", func(s *ResourceAdmissionSnapshot) { s.RequiredBytes = 0 }},
		{"changed explicit request", func(s *ResourceAdmissionSnapshot) { s.RequestedBytes = 401 }},
		{"not capacity blocked", func(s *ResourceAdmissionSnapshot) { s.RequiredBytes = 200 }},
		{"no earlier waiter", func(s *ResourceAdmissionSnapshot) { s.Reason = ResourceAdmissionFairness }},
		{"uncommitted grant", func(s *ResourceAdmissionSnapshot) { s.Reason = ResourceAdmissionGranted }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := admissionSnapshotForTest()
			test.alter(snapshot)
			if got := NormalizeResourceAdmission(snapshot); got != nil {
				t.Fatalf("invalid telemetry reached the public contract: %+v", got)
			}
		})
	}
	if NormalizeResourceAdmission(nil) != nil {
		t.Fatal("missing telemetry was invented")
	}
}

func TestResourceAdmissionProgressCloneAndMergeAreIsolated(t *testing.T) {
	source := admissionSnapshotForTest()
	cloned := Clone(Update{Phase: "waiting_for_resources", Indeterminate: true, ResourceAdmission: source})
	if cloned.ResourceAdmission == nil || cloned.ResourceAdmission == source {
		t.Fatal("snapshot was lost or aliased")
	}
	source.CapacityAvailableBytes = 1
	if cloned.ResourceAdmission.CapacityAvailableBytes != 900 {
		t.Fatal("producer mutation changed retained progress")
	}
	heartbeat := Merge(cloned, Update{Phase: "waiting_for_resources", Indeterminate: true})
	if heartbeat.ResourceAdmission == nil || heartbeat.ResourceAdmission == cloned.ResourceAdmission {
		t.Fatal("heartbeat lost or aliased its historical snapshot")
	}
	advanced := Merge(heartbeat, Update{Phase: "executing", Indeterminate: true})
	if advanced.ResourceAdmission != nil {
		t.Fatal("an old capacity decision leaked into a later unrelated phase")
	}
}

func TestResourceAdmissionObserveDeduplicatesDecisionOnHeartbeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	grant, finish := make(chan struct{}), make(chan struct{})
	var decisions []string
	heartbeatSeen := false
	_, err := Observe(ctx, time.Millisecond, func(ctx context.Context) (bool, error) {
		Report(ctx, Update{Phase: "waiting_for_resources", Indeterminate: true, ResourceAdmission: admissionSnapshotForTest()})
		select {
		case <-grant:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		admitted := admissionSnapshotForTest()
		admitted.Reason = ResourceAdmissionGranted
		admitted.NetAvailableBytes, admitted.CapacityAvailableBytes = 900, 900
		admitted.UnusedPromisedBytes, admitted.ReservedBytes = 0, 400
		Report(ctx, Update{Phase: "starting", Indeterminate: true, ResourceAdmission: admitted})
		select {
		case <-finish:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}, func(update *Update, _ time.Duration, _ int) {
		if update == nil {
			return
		}
		if admission := update.ResourceAdmission; admission != nil {
			decisions = append(decisions, admission.Reason)
			if admission.Reason == ResourceAdmissionGranted {
				close(finish)
			}
		} else if update.Phase == "waiting_for_resources" && !heartbeatSeen {
			heartbeatSeen = true
			close(grant)
		}
	})
	if err != nil || !heartbeatSeen || len(decisions) != 2 || decisions[0] != ResourceAdmissionCapacity || decisions[1] != ResourceAdmissionGranted {
		t.Fatalf("heartbeat changed or duplicated admission facts: decisions=%v heartbeat=%t err=%v", decisions, heartbeatSeen, err)
	}
}
