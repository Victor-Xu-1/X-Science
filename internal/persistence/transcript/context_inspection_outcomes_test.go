package transcript

import (
	"database/sql"
	"testing"
	"time"
)

func TestContextInspectorRetiredReclaimedOutcomeRequiresGenerationProof(t *testing.T) {
	repo, db, stream, claim := claimedComposerFixture(t, false)
	checkpoint, _, _, err := repo.AppendRunnerCheckpoint(t.Context(), AppendRunnerCheckpointInput{Claim: claim,
		ClientMessageID: "inspect-retired-checkpoint", Phase: RunnerPhaseExecuting, Resumable: true, PayloadJSON: []byte(`{"status":"running"}`)})
	if err != nil {
		t.Fatal(err)
	}
	now := repo.now().UTC().Add(2 * time.Minute)
	repo.now = func() time.Time { return now }
	if err := repo.withImmediate(t.Context(), func(conn *sql.Conn) error {
		current, err := getStreamConn(t.Context(), conn, stream.UID, stream.OwnerID)
		if err != nil {
			return err
		}
		_, err = reclaimExpiredRunnerConn(t.Context(), conn, current, claim.Attempt, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || len(before.Outcomes) != 1 || before.Outcomes[0].Status != "reclaimed" {
		t.Fatalf("current reclaimed receipt missing: %+v %v", before, err)
	}
	if _, err := db.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at)
		SELECT 'inspect-retired-resume',?,COALESCE(MAX(sequence),0)+1,'frame_resumed','{"dispatch":{"status":"claimed"}}',?
		FROM frame_events WHERE frame_id=?`, stream.FrameID, now, stream.FrameID); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "frame-resume:inspect-retired-resume", TTL: time.Minute, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
	if err != nil || !resumed.Claimed || resumed.Claim.Attempt != claim.Attempt {
		t.Fatalf("same-attempt revival=%+v %v", resumed, err)
	}
	after, err := repo.ReadContextInspection(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || len(after.Outcomes) != 1 || after.Outcomes[0].TerminalEventID != before.Outcomes[0].TerminalEventID || !after.Outcomes[0].Retired || after.OutcomeIncomplete {
		t.Fatalf("proven interruption vanished with retired receipt: %+v %v", after, err)
	}
	if len(after.Attempts) != 1 || after.Attempts[0].Status != "running" {
		t.Fatalf("historical interruption replaced current live state: %+v", after)
	}
	withoutProof := after.Attempts[0]
	withoutProof.resumeFacts = nil
	if _, proven := contextInspectionRetiredInput(withoutProof, before.Outcomes[0].TerminalEventID, after.Inputs); proven {
		t.Fatal("missing generation chain was guessed from attempt or time")
	}
}
