package workspace

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func legacyCompactionReadFixture(t *testing.T) (*sql.DB, *transcriptstore.Repository, transcriptstore.Stream) {
	t.Helper()
	db, _ := openTargetTwentyTwoProviderDB(t)
	applyTranscriptMigration(t, db, 76)
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "legacy-compaction-read", OwnerID: "legacy-compaction-owner", ExternalID: "legacy-compaction-read",
		Kind: transcriptstore.StreamKindStandalone, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, repo, stream
}

func TestManualContextCompactionLegacyReplayDoesNotInventAuthority(t *testing.T) {
	db, repo, stream := legacyCompactionReadFixture(t)
	if err := repo.ValidateContract(t.Context()); err != nil {
		t.Fatalf("supported legacy contract was rejected: %v", err)
	}
	if err := repo.ValidateCurrentContract(t.Context()); !errors.Is(err, transcriptstore.ErrSchemaUnavailable) {
		t.Fatalf("missing current admission relation was accepted: %v", err)
	}
	input := transcriptstore.ListRunnerReplayInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 5, CheckpointLimit: 5}
	if replay, err := repo.ListRunnerReplay(t.Context(), input); err != nil || len(replay) != 0 {
		t.Fatalf("legacy replay failed or invented content: %d %v", len(replay), err)
	}
	if proof, err := repo.ContextCompactionBoundaryProof(t.Context(), stream.OwnerID, stream.UID, 1); err != nil || proof != nil {
		t.Fatalf("legacy absence invented a manual boundary: %#v %v", proof, err)
	}
	// An explicit unsupported maintenance payload must not become ordinary work
	// or a valid crop simply because the prospective ledger is absent.
	if _, err := db.Exec(`INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,payload_json,created_at)
		VALUES(?,1,1,'unsupported-manual','user_message','payload',?,?)`, stream.UID,
		[]byte(`{"inputData":{"turn_intent":"compact-context"}}`), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListRunnerReplay(t.Context(), input); !errors.Is(err, transcriptstore.ErrSchemaUnavailable) {
		t.Fatalf("unverifiable maintenance replay was accepted: %v", err)
	}
	if _, err := repo.ContextCompactionBoundaryProof(t.Context(), stream.OwnerID, stream.UID, 1); !errors.Is(err, transcriptstore.ErrSchemaUnavailable) {
		t.Fatalf("unverifiable maintenance proof was accepted: %v", err)
	}
	if _, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID,
		OwnerID: stream.OwnerID, RunnerID: "legacy-maintenance", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh}); !errors.Is(err, transcriptstore.ErrSchemaUnavailable) {
		t.Fatalf("unverifiable maintenance claimed ordinary execution: %v", err)
	}
}

func TestManualContextCompactionLegacyAutoBoundaryRetainsOrdinaryInput(t *testing.T) {
	db, repo, stream := legacyCompactionReadFixture(t)
	events := []struct{ kind, payload string }{
		{"user_message", `{"role":"user","text":"Retained original task"}`},
		{"assistant_message", `{"text":"Before automatic boundary"}`},
		{"runner_checkpoint", `{"toolPhase":"auto_compact","status":"completed"}`},
		{"assistant_message", `{"text":"After automatic boundary"}`},
		{"runner_checkpoint", `{"toolPhase":"context_compact","purpose":"context_compaction","status":"completed","compaction":{"schema":"x-science.context-compaction-boundary.v1"}}`},
		{"assistant_message", `{"text":"After unproven manual-looking checkpoint"}`},
	}
	for index, event := range events {
		id := index + 1
		if _, err := db.Exec(`INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,payload_json,created_at)
			VALUES(?,?,?,?,?,'payload',?,?)`, stream.UID, id, id, fmt.Sprintf("legacy-replay:%d", id),
			event.kind, []byte(event.payload), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO transcript_branch_events(stream_uid,branch_id,ordinal,event_id)
			SELECT stream_uid,active_branch_id,?,? FROM transcript_branch_state WHERE stream_uid=?`, id, id, stream.UID); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := repo.ListRunnerReplay(t.Context(), transcriptstore.ListRunnerReplayInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 5, CheckpointLimit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	retained := map[int64]bool{}
	for _, event := range replay {
		retained[event.Event.EventID] = true
	}
	if !retained[1] || retained[2] || !retained[3] || !retained[4] || !retained[5] || !retained[6] {
		t.Fatalf("legacy auto boundary changed or unproven manual phase cropped input: %#v", retained)
	}
	if proof, err := repo.ContextCompactionBoundaryProof(t.Context(), stream.OwnerID, stream.UID, 5); err != nil || proof != nil {
		t.Fatalf("legacy phase manufactured manual authority: %#v %v", proof, err)
	}
}
