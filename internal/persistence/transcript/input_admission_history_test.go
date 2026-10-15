package transcript

import (
	"database/sql"
	"testing"
	"time"
)

func assertHistoricalInputPrefixSettled(t *testing.T, repo *Repository, db *sql.DB, uid, owner string) {
	t.Helper()
	stream, err := repo.GetStream(t.Context(), uid, owner)
	if err != nil || stream.InputRevision != stream.ConsumedInputRevision {
		t.Fatalf("historical prefix was made pending: %+v %v", stream, err)
	}
	var facts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?`, uid).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical admissions invented: %d %v", facts, err)
	}
	coverage, err := repo.GetInputCoverage(t.Context(), uid, owner, 0)
	if err != nil || !coverage.Complete || coverage.Pending() {
		t.Fatalf("historical prefix coverage=%+v err=%v", coverage, err)
	}
}

func TestCloneSettledInputKeepsHistoricalPrefixAndAdmitsOnlyNewInput(t *testing.T) {
	repo, db, source, claim := admissionFrameFixture(t)
	if _, _, _, err := repo.FinishRunner(t.Context(), FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "finish-source", Status: "completed", PayloadJSON: []byte(`{"text":"done"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO frames(id,project_id,root_frame_id,status) VALUES('clone','p','clone','completed')`); err != nil {
		t.Fatal(err)
	}
	target, err := repo.CreateStream(t.Context(), CreateStreamInput{UID: "frame:clone", OwnerID: source.OwnerID,
		ExternalID: "clone", SessionID: "clone", Kind: StreamKindFrameRef, FrameID: "clone", RootFrameID: "clone", ProjectID: source.ProjectID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := CloneFrameHistoryInput{SourceStreamUID: source.UID, TargetStreamUID: target.UID, OwnerID: source.OwnerID}
	for retry := 0; retry < 2; retry++ {
		if _, err := repo.CloneFrameHistory(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	assertHistoricalInputPrefixSettled(t, repo, db, target.UID, target.OwnerID)
	var sourceFacts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?`, source.UID).Scan(&sourceFacts); err != nil || sourceFacts != 1 {
		t.Fatalf("source admission changed: %d %v", sourceFacts, err)
	}
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: target.UID, OwnerID: target.OwnerID,
		ClientMessageID: "after-copy", MessageUUID: "after-copy", Text: "Continue the copied task", MessageOrigin: "input_response"})
	if err != nil || !created {
		t.Fatalf("new input created=%t err=%v", created, err)
	}
	var revision, sourceEvent int64
	if err := db.QueryRow(`SELECT input_revision,event_id FROM transcript_input_admissions WHERE stream_uid=?`, target.UID).Scan(&revision, &sourceEvent); err != nil || revision != 2 || sourceEvent != event.EventID {
		t.Fatalf("new admission revision=%d event=%d err=%v", revision, sourceEvent, err)
	}
	coverage, err := repo.GetInputCoverage(t.Context(), target.UID, target.OwnerID, 0)
	if err != nil || !coverage.Complete || !coverage.HasMessage || !coverage.Pending() || coverage.ConsumedInputRevision != 1 {
		t.Fatalf("copied-prefix new coverage=%+v err=%v", coverage, err)
	}
	claimed, err := repo.ClaimNextFrameRunner(t.Context(), ClaimNextFrameRunnerInput{RunnerID: "clone-worker", TTL: time.Minute})
	if err != nil || !claimed.Claimed || claimed.Claim.StreamUID != target.UID || claimed.Claim.ClaimedInputRevision != 2 {
		t.Fatalf("claim copied task=%+v err=%v", claimed, err)
	}
}

func TestLegacyFrameAdmissionRetainsExactSourceAndStreamDeletion(t *testing.T) {
	repo, db, stream, claim := admissionFrameFixture(t)
	forceLegacyFrameAuthorityForTest(t, db, stream)
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ClientMessageID: "legacy-later", FrameEventID: "legacy-later", MessageUUID: "legacy-later", Text: "Later input", MessageOrigin: "input_response"})
	if err != nil || !created || event.Source != EventSourceFrameRef {
		t.Fatalf("legacy event=%+v created=%t err=%v", event, created, err)
	}
	coverage, err := repo.GetInputCoverage(t.Context(), stream.UID, stream.OwnerID, claim.Claim.ClaimedInputRevision)
	if err != nil || !coverage.Complete || !coverage.HasMessage || !coverage.Pending() {
		t.Fatalf("legacy source coverage=%+v err=%v", coverage, err)
	}
	if _, err := db.Exec(`DELETE FROM transcript_streams WHERE stream_uid=?`, stream.UID); err != nil {
		t.Fatalf("retained-fact guard prevented authorized stream deletion: %v", err)
	}
	var facts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions WHERE stream_uid=?`, stream.UID).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("deleted stream admissions remain: %d %v", facts, err)
	}
}
