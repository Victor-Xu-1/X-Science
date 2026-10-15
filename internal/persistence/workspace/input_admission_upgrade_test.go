package workspace

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// This fixture represents an installed schema76 database, before an admission
// relation existed. SQL constructs its historical input facts; it is not a
// runtime recovery mechanism, lease rewrite, or guessed migration backfill.
func admissionLegacyV76Fixture(t *testing.T) (*Store, *transcriptstore.Repository, transcriptstore.Stream) {
	t.Helper()
	db, path := openTargetTwentyTwoProviderDB(t)
	applyTranscriptMigration(t, db, 76)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO projects(id,user_id,name,created_at,updated_at) VALUES('admission-project','admission-owner','Admission',?,?);
		INSERT INTO frames(id,incarnation_id,project_id,root_frame_id,agent_name,status,conversation_type,created_at,updated_at)
		VALUES('admission-frame','128e4f68-1d4e-4baa-8d41-84a50b10514a','admission-project','admission-frame','OPERON','processing','agent',?,?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	legacy := transcriptstore.NewRepository(db)
	stream, err := legacy.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:admission-legacy", OwnerID: "admission-owner", ExternalID: "admission-frame", SessionID: "admission-frame",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "admission-project", RootFrameID: "admission-frame", FrameID: "admission-frame", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,payload_json,created_at)
		VALUES(?,1,1,'legacy-initial','user_message','payload','{"role":"user","text":"Initial retained input"}',?);
		INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
		SELECT stream_uid,active_branch_id,1,1 FROM transcript_branch_state WHERE stream_uid=?;
		UPDATE transcript_streams SET input_revision=1,next_event_id=2,next_publication_seq=2 WHERE stream_uid=?`, stream.UID, now, stream.UID, stream.UID); err != nil {
		t.Fatal(err)
	}
	claim, err := legacy.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "legacy-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("legacy claim=%+v err=%v", claim, err)
	}
	if _, _, _, err := legacy.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "legacy-finish", Status: "completed", PayloadJSON: []byte(`{"text":"Initial result retained"}`)}); err != nil {
		t.Fatal(err)
	}
	const pending = `{"messageUuid":"legacy-late","_uuid":"legacy-late","clientMessageId":"legacy-late","role":"user","messageOrigin":"input_response","text":"Retained later input","content":[{"type":"text","text":"Retained later input"}]}`
	var eventID int64
	if err := db.QueryRow(`INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,payload_json,created_at)
		SELECT stream_uid,next_event_id,next_publication_seq,'legacy-late','user_input_response','payload',?,? FROM transcript_streams WHERE stream_uid=? RETURNING event_id`, pending, now, stream.UID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
		SELECT b.stream_uid,b.active_branch_id,(SELECT COALESCE(MAX(m.ordinal),0)+1 FROM transcript_branch_events m WHERE m.stream_uid=b.stream_uid AND m.branch_id=b.active_branch_id),?
		FROM transcript_branch_state b WHERE b.stream_uid=?;
		UPDATE transcript_streams SET input_revision=input_revision+1,next_event_id=next_event_id+1,next_publication_seq=next_publication_seq+1 WHERE stream_uid=?`, eventID, stream.UID, stream.UID); err != nil {
		t.Fatal(err)
	}
	before, err := (&Store{db: db}).SchemaStatus(t.Context())
	if err != nil || before.CurrentVersion != 76 {
		t.Fatalf("legacy schema=%+v err=%v", before, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	after, err := store.SchemaStatus(t.Context())
	if err != nil || after.CurrentVersion != 77 || len(after.Migrations) != 77 || !reflect.DeepEqual(before.Migrations, after.Migrations[:76]) {
		t.Fatalf("migration rewrote history: %+v err=%v", after, err)
	}
	if store.schemaBackupPath == "" {
		t.Fatal("schema76 backup missing")
	}
	backup, err := sql.Open(sqliteDriver, store.schemaBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	assertSchemaJournalVersion(t, backup, 76)
	var original, retained []byte
	if err := backup.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, stream.UID, eventID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, stream.UID, eventID).Scan(&retained); err != nil || !bytes.Equal(original, retained) {
		t.Fatalf("historical input changed: %v", err)
	}
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err = repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || stream.InputRevision != 2 || stream.ConsumedInputRevision != 1 {
		t.Fatalf("migration changed counters: %+v err=%v", stream, err)
	}
	var facts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("migration guessed %d historical facts: %v", facts, err)
	}
	return store, repo, stream
}

func TestInputAdmissionV77PreservesUnknownHistoryAndMigrationIdentity(t *testing.T) {
	store, repo, stream := admissionLegacyV76Fixture(t)
	coverage, err := repo.GetInputCoverage(t.Context(), stream.UID, stream.OwnerID, 1)
	if err != nil || coverage.Complete || !coverage.Pending() {
		t.Fatalf("legacy coverage=%+v err=%v", coverage, err)
	}
	if err := applyVersionedSchemaMigrations(t.Context(), store.db, time.Now); err != nil {
		t.Fatal(err)
	}
	assertSchemaJournalVersion(t, store.db, 77)
	var facts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("migration retry invented history: %d %v", facts, err)
	}
}
