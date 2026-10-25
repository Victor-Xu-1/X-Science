package server

import (
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestDelegatedStopCompletionWaitsForEveryCapturedOwner(t *testing.T) {
	for _, first := range []string{"active", "supervisor"} {
		t.Run(first, func(t *testing.T) {
			owned := newDelegatedOwnedStopFixture(t)
			if first == "active" {
				owned.releaseActive()
				awaitDelegatedOwnedExit(t, owned.activeExited)
			} else {
				owned.releaseSupervisor()
				awaitDelegatedOwnedExit(t, owned.supervisorExited)
			}
			receipt := owned.read(t)
			if receipt.State == "settled" || receipt.Targets[0].ExecutionObservation == "stopped" {
				t.Fatalf("one captured owner's exit confirmed another live owner: %#v", receipt)
			}
			owned.releaseActive()
			owned.releaseSupervisor()
			awaitDelegatedOwnedExit(t, owned.activeExited)
			awaitDelegatedOwnedExit(t, owned.supervisorExited)
			awaitDelegatedOwnedExit(t, owned.observerDone)
			receipt = owned.read(t)
			if receipt.State != "settled" || receipt.Targets[0].Outcome != "cancelled" || receipt.Targets[0].ExecutionObservation != "stopped" {
				t.Fatalf("all actual captured owners exited without original acknowledgement: %#v", receipt)
			}
		})
	}
}

func TestDelegatedStopLateSupervisorExitPreservesNewInput(t *testing.T) {
	owned := newDelegatedOwnedStopFixture(t)
	owned.releaseActive()
	awaitDelegatedOwnedExit(t, owned.activeExited)
	f, target := owned.f, owned.receipt.Targets[0].Target
	if _, _, _, err := f.repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: target.StreamUID, OwnerID: owned.child.OwnerUserID, ClientMessageID: "completion-next-input",
		FrameEventID: "completion-next-event", MessageUUID: "completion-next-uuid", Text: "A genuinely newer task", Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	newCtx, newClaim, finishNew := delegatedActiveRun(t, f, owned.child, "completion-new-owner")
	newExited := make(chan struct{})
	go func() {
		<-newCtx.Done()
		finishNew()
		close(newExited)
	}()
	t.Cleanup(func() {
		f.server.stopActiveSessionRun(owned.child.FrameID, "new owner fixture cleanup")
		awaitDelegatedOwnedExit(t, newExited)
	})
	owned.releaseSupervisor()
	awaitDelegatedOwnedExit(t, owned.supervisorExited)
	awaitDelegatedOwnedExit(t, owned.observerDone)
	receipt := owned.read(t)
	if receipt.State != "settled" || receipt.Targets[0].Target != target {
		t.Fatalf("old supervisor exit did not acknowledge only the original input: %#v", receipt)
	}
	if newCtx.Err() != nil {
		t.Fatalf("old completion cancelled a replacement owner: %v", newCtx.Err())
	}
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), newClaim); err != nil {
		t.Fatalf("old completion changed the new input claim: %v", err)
	}
}
