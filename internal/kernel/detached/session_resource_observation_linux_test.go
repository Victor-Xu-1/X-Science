//go:build linux

package detached

import (
	"context"
	"testing"
	"time"

	"x-science/internal/toolprogress"
)

func TestResourceWaitPublishesInitialAndAdmittedDecisionWithoutPollingNoise(t *testing.T) {
	b, _, backend, launcher := sessionResourceWaitFixture(t)
	// This fixture records a launch request only; it never starts an executor.
	launcher.launch = func(ExecutorLaunchRequest) error { return nil }
	updates := make(chan toolprogress.Update, 4)
	ctx, cancel := context.WithCancel(toolprogress.WithReporter(context.Background(), func(update toolprogress.Update) { updates <- update }))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.launchExecutorWithResourceWait(ctx, backend) }()
	var initial toolprogress.Update
	select {
	case initial = <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("initial capacity decision was not reported")
	}
	if initial.Phase != "waiting_for_resources" || initial.ResourceAdmission == nil || initial.ResourceAdmission.Reason != toolprogress.ResourceAdmissionCapacity || initial.ResourceAdmission.NetAvailableBytes != 0 || initial.ResourceAdmission.RequestedBytes != 0 {
		t.Fatalf("initial decision missing or changed: %+v", initial)
	}
	// Several original supervisor retries must retain the one pending call and
	// must not create another timeline state or launch while capacity is absent.
	for range 3 {
		if err := b.ReconcileStartups(ctx); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case update := <-updates:
		t.Fatalf("unchanged retries appended progress: %+v", update)
	default:
	}
	if launcher.launches.Load() != 0 {
		t.Fatal("capacity observation launched work")
	}
	launcher.available.Store(2 << 30)
	// The original bounded supervisor wraps its durable page cursor between
	// scans. Two scans reach this one generation without replacing that owner.
	for range 2 {
		if err := b.ReconcileStartups(ctx); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("original waiter did not observe the supervisor admission")
	}
	select {
	case update := <-updates:
		if update.Phase != "starting" || update.ResourceAdmission == nil || update.ResourceAdmission.Reason != toolprogress.ResourceAdmissionGranted || update.ResourceAdmission.ReservedBytes != 1<<30 || update.ResourceAdmission.CapacityAvailableBytes != 2<<30 {
			t.Fatalf("admitted progress did not read the committed original notice: %+v", update)
		}
	default:
		t.Fatal("admitted progress was not reported")
	}
	if launcher.launches.Load() != 1 || len(updates) != 0 {
		t.Fatalf("observation duplicated launch/progress: launches=%d progress=%d", launcher.launches.Load(), len(updates))
	}
}
