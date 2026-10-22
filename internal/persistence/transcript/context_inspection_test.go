package transcript

import (
	"errors"
	"testing"
	"time"
)

func TestContextInspectorAdmissionCoordinatesSurviveControlResume(t *testing.T) {
	repo, db, stream, claim := claimedComposerFixture(t, false)
	if _, err := db.Exec(`UPDATE frames SET incarnation_id='inspector-fixture-incarnation' WHERE id=?`, stream.FrameID); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimedRunnerInputContext(t.Context(), claim)
	if err != nil || first == nil || first.Association == nil {
		t.Fatalf("association missing: %+v %v", first, err)
	}
	association := first.Association
	if association.Scope.FrameIncarnationID == "" || association.InputRevision != 1 || association.ClaimedInputRevision != 1 || association.RunnerAttempt != claim.Attempt {
		t.Fatalf("incomplete immutable coordinates: %+v", association)
	}
	checkpoint, _, _, err := repo.PauseRunnerForInput(t.Context(), AppendRunnerCheckpointInput{Claim: claim,
		ClientMessageID: "inspect-question", Phase: RunnerPhaseWaitingUser, Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
			FrameID: stream.FrameID, ClientMessageID: "inspect-answer", PayloadJSON: []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"inspect-question","content":"yes"}]}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "inspect-resumed",
		TTL: time.Minute, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
	if err != nil || !resumed.Claimed {
		t.Fatalf("resume=%+v %v", resumed, err)
	}
	current, err := repo.ClaimedRunnerInputContext(t.Context(), resumed.Claim)
	if err != nil || current.Association == nil || current.Association.InputEventID != association.InputEventID ||
		current.Association.InputRevision != association.InputRevision || current.Association.ClaimedInputRevision <= association.ClaimedInputRevision {
		t.Fatalf("control response became an ordinary prompt: %+v %v", current, err)
	}
}

func TestContextInspectorCommittedTerminalAndScopeFence(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || len(before.Inputs) != 1 || len(before.Attempts) != 1 || before.Attempts[0].Status != "running" {
		t.Fatalf("initial projection=%+v err=%v", before, err)
	}
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); err != nil {
		t.Fatal(err)
	}
	terminal, receipt, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim,
		ClientMessageID: "inspect-terminal", Status: "failed", PayloadJSON: []byte(`{"detail":"synthetic failure"}`)})
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || len(after.Attempts) != 1 || after.Attempts[0].Status != "failed" || after.Attempts[0].TerminalEventID != terminal.EventID ||
		after.Attempts[0].CompletedAt == nil || !after.Attempts[0].CompletedAt.Equal(receipt.FinishedAt) {
		t.Fatalf("committed receipt missing: %+v err=%v", after, err)
	}
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); !errors.Is(err, ErrBranchStateStale) {
		t.Fatalf("mixed publication read accepted: %v", err)
	}
	wrong := after.Scope
	wrong.FrameIncarnationID = "another-incarnation"
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, wrong); !errors.Is(err, ErrBranchStateStale) {
		t.Fatalf("incarnation mismatch accepted: %v", err)
	}
	if _, err := repo.ReadContextInspection(t.Context(), stream.UID, "foreign-owner"); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("owner isolation lost: %v", err)
	}
}

func TestContextInspectorExcludesAbandonedBranchTerminal(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim, ClientMessageID: "inspect-branch-finish", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ForkFrameUserMessageBranch(t.Context(), ForkFrameUserMessageBranchInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		SourceBranchID: before.Scope.BranchID, ExpectedActiveBranchID: before.Scope.BranchID, ExpectedGeneration: before.Scope.BranchGeneration,
		ClientMutationID: "inspect-fork", SourceClientMessageID: "selected", SourceMessageIndex: 0, ReplacementText: "A distinct new prompt", Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || len(after.Inputs) != 1 || after.Inputs[0].EventID == before.Inputs[0].EventID || len(after.Attempts) != 0 {
		t.Fatalf("abandoned branch leaked into projection: %+v err=%v", after, err)
	}
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); !errors.Is(err, ErrBranchStateStale) {
		t.Fatalf("old branch remained valid: %v", err)
	}
}
