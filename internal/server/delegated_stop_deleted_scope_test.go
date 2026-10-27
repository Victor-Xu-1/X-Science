package server

import (
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopDeletedScopeRetiresProofWithoutAcknowledgingIt(t *testing.T) {
	for _, deleted := range []string{"parent_conversation", "project"} {
		t.Run(deleted, func(t *testing.T) {
			owned := newDelegatedOwnedStopFixture(t)
			f := owned.f
			if _, err := f.db.Exec(`CREATE TRIGGER reject_deleted_scope_observation BEFORE UPDATE ON frame_events WHEN OLD.event_type='delegated_stop_receipt' BEGIN SELECT RAISE(ABORT,'controlled transient storage unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			owned.releaseActive()
			owned.releaseSupervisor()
			awaitDelegatedOwnedExit(t, owned.activeExited)
			awaitDelegatedOwnedExit(t, owned.supervisorExited)
			awaitDelegatedOwnedExit(t, owned.observerDone)
			before := owned.read(t)
			if before.State == "settled" || before.Targets[0].ExecutionObservation == "stopped" {
				t.Fatalf("real rejected write was misreported as an ACK: %#v", before)
			}
			proofs := f.server.pendingDelegatedStopProofs(owned.child.OwnerUserID, before)
			if len(proofs) != 1 || proofs[0].result() == nil || delegatedStopProofRetired(proofs[0].result()) {
				t.Fatalf("real storage rejection was discarded as scope retirement: count=%d", len(proofs))
			}
			proof := proofs[0]
			if _, err := f.repo.CancelRunner(t.Context(), transcriptstore.CancelRunnerInput{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ExpectedAttempt: f.claim.Attempt,
				ClientMessageID: "deleted-scope-parent-finished", ReasonCode: "owned_fixture_complete", Destinations: []string{"ws"},
			}); err != nil {
				t.Fatal(err)
			}
			if count, err := f.repo.CountActiveRunnerAttempts(t.Context()); err != nil || count != 0 {
				t.Fatalf("unrelated durable owner invalidates drain oracle: count=%d err=%v", count, err)
			}
			if f.server.TryDrainIdle() || !f.server.hasPendingDelegatedStopProofs() {
				t.Fatal("genuine storage failure stopped protecting its completed proof")
			}
			if _, err := f.db.Exec(`DROP TRIGGER reject_deleted_scope_observation`); err != nil {
				t.Fatal(err)
			}
			if deleted == "parent_conversation" {
				if _, err := f.store.DeleteCompatibilityFrameTree(before.Request.Scope.ParentFrameID, f.stream.OwnerID,
					before.Request.Scope.ParentFrameIncarnationID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.store.DeleteProjectRealtime(t.Context(), f.identity.access.Frame.ProjectID,
					f.stream.OwnerID, "deleted-proof-project"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.store.RecordDelegatedStopObservation(t.Context(), f.stream.OwnerID, proof.observed); !errors.Is(err, workspace.ErrDelegatedStopUnavailable) {
				t.Fatalf("canonical deleted scope did not produce its precise domain error: %v", err)
			}
			if !f.server.TryDrainIdle() || f.server.hasPendingDelegatedStopProofs() {
				t.Fatal("authoritatively deleted scope permanently blocked normal idle reload")
			}
			if !errors.Is(proof.result(), workspace.ErrDelegatedStopUnavailable) || proof.observed.State == "settled" {
				t.Fatal("retiring unavailable metadata fabricated successful completion")
			}
			if _, _, err := f.store.ReadDelegatedStop(t.Context(), f.stream.OwnerID, before.Request.Scope.ParentFrameID,
				before.Request.RequestID); !errors.Is(err, workspace.ErrDelegatedStopUnavailable) {
				t.Fatalf("deleted receipt was silently recreated or acknowledged: %v", err)
			}
		})
	}
}
