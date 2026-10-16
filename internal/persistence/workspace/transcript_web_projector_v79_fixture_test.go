package workspace

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Install the real historical registry through78, then seed source through
// normal Store/Repository writers. Only the derived rows are a controlled v13
// migration fixture; serving/reduction correctness is tested by the server.
func newTranscriptWebV78Database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, path := openTargetTwentyTwoProviderDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	applyTranscriptMigration(t, db, 78)
	store := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	if _, err := store.CreateProject(CreateProjectInput{ID: "project-v79", UserID: "owner-v79", Name: "v79"}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(CreateFrameInput{
		ID: "frame-v79", ProjectID: "project-v79", AgentName: "agent", Status: "pending", ConversationType: "execution",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
		ArtifactID: "artifact-v79", ProjectID: frame.ProjectID, Name: "retained.txt", Kind: "text/plain",
		Content: []byte("retained artifact bytes"), CreatedBy: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "stream-v79", OwnerID: "owner-v79", ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	inputRefs := []transcriptstore.UserArtifactReferenceInput{{ArtifactID: artifact.ID, VersionID: version.ID}}
	refs, err := repo.ValidateUserArtifactReferences(t.Context(), stream.UID, stream.OwnerID, inputRefs)
	if err != nil || len(refs) != 1 {
		t.Fatalf("actual input artifact reference=%+v err=%v", refs, err)
	}
	event, _, created, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "client-v79", FrameEventID: "event-v79",
		MessageUUID: "message-v79", Text: "preserve this attached source", ArtifactReferences: inputRefs,
	})
	if err != nil || !created {
		t.Fatalf("actual source append created=%t err=%v", created, err)
	}
	snapshot, err := repo.GetProjectionSnapshot(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{"id": "message-v79", "msg_id": "client-v79", "conversation_id": frame.ID,
		"type": "text", "position": "right", "status": "finish", "created_at": event.CreatedAt.UnixMilli(),
		"content": map[string]any{"content": "preserve this attached source"}})
	if err != nil {
		t.Fatal(err)
	}
	const stateJSON = `{"version":1}`
	if _, err := db.Exec(`INSERT INTO transcript_web_projection_state(
		stream_uid,branch_id,branch_generation,projector_version,projection_revision,
		through_publication_seq,source_revision,message_count,visible_message_count,
		message_artifact_reference_count,projector_state_json,projector_state_sha256,source_chain_sha256,status,last_error_code,updated_at
	) VALUES(?,?,?,13,1,?,0,1,1,1,?,?,?,'ready','',CURRENT_TIMESTAMP)`,
		stream.UID, snapshot.BranchID, snapshot.BranchGeneration, snapshot.ThroughPublicationSequence,
		stateJSON, transcriptstore.TranscriptWebSHA256([]byte(stateJSON)), transcriptstore.TranscriptWebSHA256(event.PayloadJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_web_messages(
		stream_uid,branch_id,ordinal,message_id,client_message_id,visible,visible_index,message_json,message_sha256,
		first_publication_seq,last_publication_seq,updated_at
	) VALUES(?,?,1,'message-v79','client-v79',1,0,?,?,?, ?,CURRENT_TIMESTAMP)`,
		stream.UID, snapshot.BranchID, string(message), transcriptstore.TranscriptWebSHA256(message), event.PublicationSeq, event.PublicationSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_web_message_identities(stream_uid,branch_id,identity,message_ordinal,kind)
		VALUES(?,?,'message-v79',1,'message_id'),(?,?,'client-v79',1,'client_message_id')`,
		stream.UID, snapshot.BranchID, stream.UID, snapshot.BranchID); err != nil {
		t.Fatal(err)
	}
	ref := refs[0]
	if _, err := db.Exec(`INSERT INTO transcript_web_message_artifact_refs(
		stream_uid,branch_id,message_ordinal,ordinal,source_event_id,runner_attempt,source_reference_ordinal,
		artifact_id,version_id,relation,filename,content_type,size_bytes,checksum
	) VALUES(?,?,1,0,?,NULL,0,?,?,'attached',?,?,?,?)`,
		stream.UID, snapshot.BranchID, event.EventID, ref.ArtifactID, ref.VersionID, ref.Filename, ref.ContentType, ref.SizeBytes, ref.Checksum); err != nil {
		t.Fatal(err)
	}
	assertTranscriptWebV79Integrity(t, db, 78)
	return db, path
}
