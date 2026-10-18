package kernel

import (
	"errors"
	"testing"
	"time"
)

func TestManualKernelPersistenceRequiredRetainsAllTerminalReceipts(t *testing.T) {
	for _, dequeued := range []bool{false, true} {
		t.Run(map[bool]string{false: "executed", true: "dequeued"}[dequeued], func(t *testing.T) {
			manager, _, request := manualAdmissionManagerFixture(t)
			gate := make(chan error, 1)
			request.StartAuthorization = gate
			request.PersistenceRequired = true
			handle, err := manager.Submit(request)
			if err != nil {
				t.Fatal(err)
			}
			handle.AcknowledgePersistence()
			if manager.ActiveExecutionCount() != 1 {
				t.Fatal("early ACK released a queued invocation")
			}
			if dequeued {
				gate <- errors.New("admission rejected")
			} else {
				handle.execution.finish(ExecutionOutcome{Response: Response{ID: request.ExecID, Stdout: "fixture"}, StartedAt: time.Now().UTC()})
			}
			close(gate)
			outcome := waitForLifecycleOutcome(t, handle)
			if outcome.Dequeued != dequeued {
				t.Fatalf("outcome=%#v", outcome)
			}
			if manager.ActiveExecutionCount() != 1 {
				t.Fatal("terminal result released before persistence")
			}
			handle.AcknowledgePersistence()
			handle.AcknowledgePersistence()
			if manager.ActiveExecutionCount() != 0 {
				t.Fatal("terminal ACK did not release exactly once")
			}
		})
	}
}

func TestManualKernelIdentityIsImmutableAndPersistenceIsNotBackground(t *testing.T) {
	manager, _, request := manualAdmissionManagerFixture(t)
	request.PersistenceRequired, request.Background = true, true
	if _, err := manager.Submit(request); err == nil {
		t.Fatal("manual persistence was combined with Background")
	}
	request.Background = false
	gate := make(chan error, 1)
	request.StartAuthorization = gate
	handle, err := manager.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	identity := handle.Identity()
	request.Code = "different source"
	identity.KernelID, identity.Source = "different kernel", "different source"
	actual := handle.Identity()
	if actual.KernelID != "manual-kernel" || actual.Source != "print('must not run')" || actual.KernelGeneration != 7 || actual.FrameIncarnationID != "frame-incarnation" || actual.RootFrameID != "manual-frame" {
		t.Fatalf("registration identity was not frozen: %#v", actual)
	}
	gate <- errors.New("test cleanup")
	close(gate)
	_ = waitForLifecycleOutcome(t, handle)
	handle.AcknowledgePersistence()
}
