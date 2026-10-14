//go:build linux

package detached

import (
	"context"
	"errors"
	"reflect"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestReleasedResourceWaitTerminalReplayPreservesReceiptWithoutObservation(t *testing.T) {
	b, _, starting, launcher := resourceReplacementFixture(t)
	ctx := context.Background()
	if cancelled, err := b.Store.CancelWaitingKernelResources(ctx, starting.BackendID, starting.BackendGeneration); err != nil || !cancelled {
		t.Fatalf("release fixture: cancelled=%t err=%v", cancelled, err)
	}
	if handled, err := b.settleReleasedResourceWait(ctx, starting); err != nil || !handled {
		t.Fatalf("initial non-launch retirement: handled=%t err=%v", handled, err)
	}
	settled, found, err := b.Store.GetKernelExecutionBackend(ctx, starting.BackendID)
	if err != nil || !found || settled.State != workspace.KernelExecutionBackendStateStopped {
		t.Fatalf("terminal backend missing: %+v found=%t err=%v", settled, found, err)
	}
	launcher.observeHook = func(context.Context) error {
		t.Error("terminal non-launch receipt caused another supervisor observation")
		return errors.New("terminal observation must not run")
	}
	// A supervisor may retain its earlier Starting snapshot after another
	// consumer commits retirement. It must recognize that exact terminal proof
	// as handled so a launch-error fallback cannot fabricate failure evidence.
	if handled, err := b.reconcileResourceWait(ctx, starting); err != nil || !handled {
		t.Fatalf("stale starting replay: handled=%t err=%v", handled, err)
	}
	// A caller already holding the terminal row has no startup work to reconcile.
	if handled, err := b.reconcileResourceWait(ctx, settled); err != nil || handled {
		t.Fatalf("terminal entry replay: handled=%t err=%v", handled, err)
	}
	if err := b.settleExitedStartup(ctx, starting); err != nil {
		t.Fatalf("startup settlement ignored the existing non-launch receipt: %v", err)
	}
	if err := b.launchExecutor(starting); !errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
		t.Fatalf("stale launch did not retain its admission rejection: %v", err)
	}
	wrong := starting
	wrong.ExecutorInstanceID = "other-executor"
	if handled, err := b.settleReleasedResourceWait(ctx, wrong); !handled || !errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
		t.Fatalf("terminal proof bypassed exact identity: handled=%t err=%v", handled, err)
	}
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, starting.BackendID)
	if err != nil || !found || !reflect.DeepEqual(current, settled) {
		t.Fatalf("terminal replay changed the durable backend: %+v found=%t err=%v", current, found, err)
	}
	assertReleasedResourceWaitStoppedWithoutFailure(t, b, starting)
	if launcher.observeHook == nil || len(launcher.launches) != 0 {
		t.Fatalf("terminal replay observed or launched a process: hookPresent=%t launches=%d", launcher.observeHook != nil, len(launcher.launches))
	}
}
