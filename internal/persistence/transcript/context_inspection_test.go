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
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); err != nil {
		t.Fatalf("committed terminal invalidated an already verified as-of projection: %v", err)
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

func TestContextInspectorAsOfSnapshotSurvivesOrdinaryPublication(t *testing.T) {
	repo, _, stream, claim := claimedComposerFixture(t, false)
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendRunnerCheckpoint(t.Context(), AppendRunnerCheckpointInput{Claim: claim,
		ClientMessageID: "ordinary-inspection-progress", Phase: RunnerPhaseExecuting, Resumable: true, PayloadJSON: []byte(`{"status":"running"}`)}); err != nil {
		t.Fatal(err)
	}
	after, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || after.Scope.ThroughPublicationSequence <= before.Scope.ThroughPublicationSequence {
		t.Fatalf("ordinary progress did not advance its journal watermark: %+v %v", after.Scope, err)
	}
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); err != nil {
		t.Fatalf("ordinary same-identity progress invalidated a verified as-of snapshot: %v", err)
	}
}

func TestContextInspectorAsOfIdentityAndWatermarkFences(t *testing.T) {
	repo, _, stream, _ := claimedComposerFixture(t, false)
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*ContextInspectionScope)
	}{
		{"conversation", func(s *ContextInspectionScope) { s.ConversationID = "another-frame" }},
		{"incarnation", func(s *ContextInspectionScope) { s.FrameIncarnationID = "another-incarnation" }},
		{"stream", func(s *ContextInspectionScope) { s.StreamUID = "frame:another" }},
		{"epoch", func(s *ContextInspectionScope) { s.StreamEpoch++ }},
		{"branch", func(s *ContextInspectionScope) { s.BranchID = "br_another" }},
		{"generation", func(s *ContextInspectionScope) { s.BranchGeneration++ }},
		{"future-watermark", func(s *ContextInspectionScope) { s.ThroughPublicationSequence++ }},
		{"negative-watermark", func(s *ContextInspectionScope) { s.ThroughPublicationSequence = -1 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrong := before.Scope
			c.change(&wrong)
			if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, wrong); err == nil {
				t.Fatal("changed identity or unavailable journal coordinate accepted")
			}
		})
	}
}

func TestContextInspectorActiveStreamCutoverInvalidatesOldReads(t *testing.T) {
	repo, db, stream, claim := claimedComposerFixture(t, true)
	createHistoryRealtimeTestTable(t, db)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim,
		ClientMessageID: "inspection-activation-finish", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE frames SET status='completed',incarnation_id='inspection-activation-incarnation' WHERE id=?`, stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE transcript_delivery_intents SET status='delivered',delivered_at=updated_at
		WHERE stream_uid=? AND status IN ('pending','inflight','failed')`, stream.UID); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	audit, _, err := repo.auditOrdinaryFrameHistory(t.Context(), stream.UID, stream.OwnerID, before.Scope.BranchID)
	if err != nil {
		t.Fatal(err)
	}
	activated, created, err := repo.ActivateOrdinaryFrameHistory(t.Context(), audit.RunID, stream.UID, stream.OwnerID)
	if err != nil || !created || activated.TargetStreamUID == stream.UID || activated.TargetEpoch != stream.Epoch+1 {
		t.Fatalf("normal activation did not replace the active stream/epoch: %+v created=%v err=%v", activated, created, err)
	}
	t.Run("initial-read", func(t *testing.T) {
		if _, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID); !errors.Is(err, ErrBranchStateStale) {
			t.Fatalf("obsolete active stream classified as a retainable snapshot conflict: %v", err)
		}
	})
	t.Run("final-fence", func(t *testing.T) {
		if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, before.Scope); !errors.Is(err, ErrBranchStateStale) {
			t.Fatalf("obsolete verified scope classified as a retainable snapshot conflict: %v", err)
		}
	})
	current, err := repo.ReadContextInspection(t.Context(), activated.TargetStreamUID, stream.OwnerID)
	if err != nil || current.Scope.StreamUID != activated.TargetStreamUID || current.Scope.StreamEpoch != activated.TargetEpoch {
		t.Fatalf("new active identity is unreadable: %+v err=%v", current.Scope, err)
	}
	if err := repo.ValidateContextInspection(t.Context(), stream.OwnerID, current.Scope); err != nil {
		t.Fatal(err)
	}
}
