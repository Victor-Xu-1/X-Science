//go:build linux

package detached

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestResourceWaitReplacementKeepsUnprovenSupervisor(t *testing.T) {
	for _, observation := range []ExecutorLaunchState{ExecutorLaunchAlive, ExecutorLaunchUnknown} {
		t.Run(string(observation), func(t *testing.T) {
			b, spec, previous, launcher := resourceReplacementFixture(t)
			launcher.observation = observation
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			spec.ResourceMemoryBytes = 1 << 30
			if _, err := b.EnsureSession(ctx, spec); err == nil || errors.Is(err, launcher.launchError) {
				t.Fatalf("unproven predecessor allowed replacement: %v", err)
			}
			current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
			if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStarting || current.StateVersion != previous.StateVersion {
				t.Fatalf("unproven predecessor was mutated: %+v found=%t err=%v", current, found, err)
			}
			reservation, found, err := b.Store.KernelResourceReservation(ctx, previous.BackendID, previous.BackendGeneration)
			if err != nil || !found || reservation.State != "waiting" || len(launcher.launches) != 0 {
				t.Fatalf("unproven reservation was released or replacement launched: %+v launches=%+v found=%t err=%v", reservation, launcher.launches, found, err)
			}
		})
	}
}

func TestResourceWaitReplacementAdmissionWinnerCannotBeFinished(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ticks, err := kernelruntime.CurrentProcessStartTicks()
	if err != nil {
		t.Fatal(err)
	}
	launcher.observeHook = func(ctx context.Context) error {
		reservation, admitted, err := b.Store.AdmitKernelResources(ctx, previous, 0,
			workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes})
		if err != nil || !admitted || reservation.State != "reserved" {
			t.Fatalf("competing admission did not win: %+v admitted=%t err=%v", reservation, admitted, err)
		}
		_, err = b.Store.ActivateKernelExecutionBackend(ctx, workspace.ActivateKernelExecutionBackendInput{
			BackendID: previous.BackendID, BackendGeneration: previous.BackendGeneration, ExecutorInstanceID: previous.ExecutorInstanceID,
			ExecutorPID: int64(os.Getpid()), ExecutorPIDStartTicks: ticks,
			WorkerPID: int64(os.Getpid()), WorkerPIDStartTicks: ticks, WorkerPGID: int64(os.Getpid()), HeartbeatSequence: 1,
		})
		return err
	}
	spec.ResourceMemoryBytes = 1 << 30
	if retired, err := b.retireUnlaunchedResourceWait(ctx, previous, durableSessionSpec(spec)); err != nil || retired {
		t.Fatalf("admission-winning ready predecessor was finished: retired=%t err=%v", retired, err)
	}
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || current.State != workspace.KernelExecutionBackendStateReady || current.ExecutorPID != int64(os.Getpid()) || current.ExecutorPIDStartTicks != ticks || current.WorkerPID != int64(os.Getpid()) || current.HeartbeatAt == nil {
		t.Fatalf("admitted process ownership was finished or replaced: %+v found=%t err=%v", current, found, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(ctx, previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "reserved" || reservation.ReservedBytes <= 0 || len(launcher.launches) != 0 {
		t.Fatalf("admission winner lost its reservation or replacement launched: %+v launches=%+v found=%t err=%v", reservation, launcher.launches, found, err)
	}
}

func TestResourceWaitReplacementRejectsStaleAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*workspace.KernelExecutionBackend)
	}{
		{"boot", func(b *workspace.KernelExecutionBackend) { b.MachineBootID = "other-boot" }},
		{"generation", func(b *workspace.KernelExecutionBackend) { b.BackendGeneration++ }},
		{"executor", func(b *workspace.KernelExecutionBackend) { b.ExecutorInstanceID = "other-executor" }},
		{"spec", func(b *workspace.KernelExecutionBackend) { b.SessionSpecSHA256 = "different-spec" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, spec, previous, _ := resourceReplacementFixture(t)
			expected := previous
			tc.change(&expected)
			spec.ResourceMemoryBytes = 1 << 30
			if retired, err := b.retireUnlaunchedResourceWait(context.Background(), expected, durableSessionSpec(spec)); retired || !errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
				t.Fatalf("stale identity authorized retirement: retired=%t err=%v", retired, err)
			}
			reservation, found, err := b.Store.KernelResourceReservation(context.Background(), previous.BackendID, previous.BackendGeneration)
			if err != nil || !found || reservation.State != "waiting" {
				t.Fatalf("stale identity changed resource authority: %+v found=%t err=%v", reservation, found, err)
			}
		})
	}
}

func TestResourceWaitReplacementKeepsPreviouslyAdmittedGeneration(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx := context.Background()
	if _, admitted, err := b.Store.AdmitKernelResources(ctx, previous, 0,
		workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}); err != nil || !admitted {
		t.Fatalf("fixture did not admit the original generation: admitted=%t err=%v", admitted, err)
	}
	if err := b.Store.ReleaseKernelResources(ctx, previous.BackendID, previous.BackendGeneration, previous.MachineBootID); err != nil {
		t.Fatal(err)
	}
	spec.ResourceMemoryBytes = 1 << 30
	if retired, err := b.retireUnlaunchedResourceWait(ctx, previous, durableSessionSpec(spec)); err != nil || retired {
		t.Fatalf("previously admitted generation was called never-launched: retired=%t err=%v", retired, err)
	}
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || current.State != previous.State || current.StateVersion != previous.StateVersion || len(launcher.launches) != 0 {
		t.Fatalf("previously admitted backend was mutated: %+v found=%t err=%v", current, found, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(ctx, previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "released" || reservation.ReservedBytes <= 0 {
		t.Fatalf("previously admitted resource evidence was lost: %+v found=%t err=%v", reservation, found, err)
	}
}

func TestResourceWaitReplacementRechecksPhysicalClaim(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx := context.Background()
	ticks, err := kernelruntime.CurrentProcessStartTicks()
	if err != nil {
		t.Fatal(err)
	}
	launcher.observeHook = func(ctx context.Context) error {
		// Even an inconsistent supervisor report cannot override a physical
		// claim that becomes visible before the final retirement check.
		return b.Store.ClaimKernelExecutorStartup(ctx, workspace.ActivateKernelExecutionBackendInput{
			BackendID: previous.BackendID, BackendGeneration: previous.BackendGeneration, ExecutorInstanceID: previous.ExecutorInstanceID,
			ExecutorPID: int64(os.Getpid()), ExecutorPIDStartTicks: ticks,
		})
	}
	spec.ResourceMemoryBytes = 1 << 30
	if retired, err := b.retireUnlaunchedResourceWait(ctx, previous, durableSessionSpec(spec)); err != nil || retired {
		t.Fatalf("new physical claim was finished: retired=%t err=%v", retired, err)
	}
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStarting || current.ExecutorPID != int64(os.Getpid()) || current.ExecutorPIDStartTicks != ticks {
		t.Fatalf("physical claim was changed: %+v found=%t err=%v", current, found, err)
	}
}

func TestResourceWaitReplacementObserverErrorKeepsAuthority(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	observeErr := errors.New("supervisor observation unavailable")
	launcher.observeHook = func(context.Context) error { return observeErr }
	spec.ResourceMemoryBytes = 1 << 30
	if retired, err := b.retireUnlaunchedResourceWait(context.Background(), previous, durableSessionSpec(spec)); retired || !errors.Is(err, observeErr) {
		t.Fatalf("observer error was mistaken for exit: retired=%t err=%v", retired, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(context.Background(), previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "waiting" {
		t.Fatalf("observer error cancelled resource authority: %+v found=%t err=%v", reservation, found, err)
	}
}

func TestResourceWaitReplacementPreservesMatchingSpecification(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	launcher.observeHook = func(context.Context) error { return errors.New("matching allocation must not be retired") }
	if retired, err := b.retireUnlaunchedResourceWait(context.Background(), previous, durableSessionSpec(spec)); retired || err != nil {
		t.Fatalf("matching specification authorized retirement: retired=%t err=%v", retired, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(context.Background(), previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "waiting" || launcher.observeHook == nil {
		t.Fatalf("matching specification lost its existing launch authority: %+v found=%t err=%v", reservation, found, err)
	}
}

func TestResourceWaitReplacementRequiresNoPhysicalEvidence(t *testing.T) {
	previous := workspace.KernelExecutionBackend{State: workspace.KernelExecutionBackendStateStarting}
	if !unlaunchedResourceWaitBackend(previous) {
		t.Fatal("starting backend without physical evidence was excluded")
	}
	for _, tc := range []struct {
		name   string
		change func(*workspace.KernelExecutionBackend)
	}{
		{"ready", func(b *workspace.KernelExecutionBackend) { b.State = workspace.KernelExecutionBackendStateReady }},
		{"executor_pid", func(b *workspace.KernelExecutionBackend) { b.ExecutorPID = 1 }},
		{"executor_ticks", func(b *workspace.KernelExecutionBackend) { b.ExecutorPIDStartTicks = 1 }},
		{"worker_pid", func(b *workspace.KernelExecutionBackend) { b.WorkerPID = 1 }},
		{"worker_ticks", func(b *workspace.KernelExecutionBackend) { b.WorkerPIDStartTicks = 1 }},
		{"worker_pgid", func(b *workspace.KernelExecutionBackend) { b.WorkerPGID = 1 }},
		{"cgroup", func(b *workspace.KernelExecutionBackend) { b.CgroupPath = "/owned" }},
		{"heartbeat_sequence", func(b *workspace.KernelExecutionBackend) { b.HeartbeatSequence = 1 }},
		{"heartbeat_time", func(b *workspace.KernelExecutionBackend) { now := time.Now(); b.HeartbeatAt = &now }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := previous
			tc.change(&current)
			if unlaunchedResourceWaitBackend(current) {
				t.Fatal("physical ownership was mistaken for an unlaunched resource wait")
			}
		})
	}
}
