package workspace

import (
	"encoding/json"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Reconstruct the reviewed schema76 writer, not a lease/status manipulation:
// its normal second composer input was user_input_response, incremented U and
// activated a completed Frame. No prospective admission relation existed yet.
func appendSchema76QueuedFrameInput(t *testing.T, store *Store, repo *transcriptstore.Repository, stream transcriptstore.Stream) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"messageUuid": "queued", "_uuid": "queued", "clientMessageId": "queued",
		"role": "user", "messageOrigin": "input_response", "text": "Continue the retained task",
		"content": []any{map[string]any{"type": "text", "text": "Continue the retained task"}}})
	if err != nil {
		t.Fatal(err)
	}
	staged, err := repo.StageUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ClientMessageID: "queued", PayloadJSON: payload, Destinations: []string{"ws"}})
	if err != nil || !staged.Created {
		t.Fatalf("stage historical composer=%+v %v", staged, err)
	}
	err = repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_events SET event_type='user_input_response'
			WHERE stream_uid=? AND event_id=? AND event_type='user_message_staged'`, stream.UID, staged.Event.EventID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_streams SET input_revision=input_revision+1,updated_at=? WHERE stream_uid=?`, staged.Event.CreatedAt, stream.UID); err != nil {
			return err
		}
		// Exactly the original activateFrameForNewInputConn transition. This is
		// the historical writer's behavior, not a synthetic recovery status.
		if _, err := tx.ExecContext(t.Context(), `UPDATE frames SET status='processing',updated_at=? WHERE id=?
			AND status IN ('completed','failed','cancelled','canceled','awaiting_user_response','awaiting_plan_approval')`, staged.Event.CreatedAt, stream.FrameID); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO frame_runtime_metadata(frame_id,status_description,completed_at,context_data)
			VALUES(?,'',NULL,'{}') ON CONFLICT(frame_id) DO UPDATE SET status_description='',completed_at=NULL`, stream.FrameID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func schema76QueuedInputUpgrade(t *testing.T, frame, prepared bool) (*Store, *transcriptstore.Repository, transcriptstore.Stream) {
	t.Helper()
	db, path := openTargetTwentyTwoProviderDB(t)
	applyTranscriptMigration(t, db, 76)
	oldStore := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	repo := transcriptstore.NewRepository(db)
	input := transcriptstore.CreateStreamInput{UID: "legacy-queued", OwnerID: "owner", ExternalID: "legacy-queued",
		SessionID: "legacy-queued", Kind: transcriptstore.StreamKindStandalone, Epoch: 1}
	if frame {
		if _, err := oldStore.CreateProject(CreateProjectInput{ID: "queued-project", UserID: "owner", Name: "Historical queued input"}); err != nil {
			t.Fatal(err)
		}
		created, err := oldStore.CreateFrame(CreateFrameInput{ID: "queued-frame", ProjectID: "queued-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
		if err != nil {
			t.Fatal(err)
		}
		input.UID, input.ExternalID, input.SessionID = "frame:queued-frame", created.ID, created.ID
		input.Kind, input.ProjectID, input.FrameID, input.RootFrameID = transcriptstore.StreamKindFrameRef, created.ProjectID, created.ID, created.RootFrameID
	}
	stream, err := repo.CreateStream(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if frame {
		if _, created, err := appendHistoricalMigrationFrameUserEvent(t, t.Context(), db, repo, transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", FrameEventID: "initial-frame", MessageUUID: "initial", Text: "Initial task"}); err != nil || !created {
			t.Fatalf("initial composer created=%t %v", created, err)
		}
	} else {
		if _, created, err := appendHistoricalMigrationUserEvent(t, t.Context(), db, repo, transcriptstore.AppendUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial", PayloadJSON: []byte(`{"text":"Initial task"}`)}); err != nil || !created {
			t.Fatalf("initial input created=%t %v", created, err)
		}
		if _, _, err := repo.ActivateDeliveryRoute(t.Context(), stream.OwnerID, stream.UID, stream.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	ttl := time.Minute
	if prepared {
		ttl = 2 * time.Second
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "schema76-owner", TTL: ttl, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("schema76 claim=%+v %v", claim, err)
	}
	if !prepared {
		if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claim.Claim,
			ClientMessageID: "finished-before-upgrade", Status: "completed", PayloadJSON: []byte(`{"text":"Initial result"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if frame {
		appendSchema76QueuedFrameInput(t, oldStore, repo, stream)
	} else {
		if _, created, err := appendHistoricalMigrationUserEvent(t, t.Context(), db, repo, transcriptstore.AppendUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "queued", PayloadJSON: []byte(`{"text":"Continue the retained task"}`)}); err != nil || !created {
			t.Fatalf("queued input created=%t %v", created, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err = store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err = repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
	wantConsumed := int64(1)
	if prepared {
		wantConsumed = 0
	}
	if err != nil || stream.InputRevision != 2 || stream.ConsumedInputRevision != wantConsumed {
		t.Fatalf("upgrade changed input=%+v %v", stream, err)
	}
	var facts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_input_admissions`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical facts invented=%d %v", facts, err)
	}
	if frame {
		current, found, err := store.GetFrame(stream.FrameID)
		if err != nil || !found || current.Status != FrameStatusProcessing {
			t.Fatalf("old writer activation lost=%+v %v", current, err)
		}
	}
	if prepared {
		<-time.NewTimer(time.Until(claim.Claim.ExpiresAt) + time.Millisecond).C
	}
	return store, repo, stream
}

func TestSchema76QueuedFrameInputSurvivesUpgrade(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_finish", true: "prepared_inflight"}[prepared], func(t *testing.T) {
			_, repo, stream := schema76QueuedInputUpgrade(t, true, prepared)
			claimed, err := repo.ClaimNextFrameRunner(t.Context(), transcriptstore.ClaimNextFrameRunnerInput{RunnerID: "upgraded-owner", TTL: time.Minute})
			if err != nil || !claimed.Claimed || claimed.Claim.StreamUID != stream.UID || claimed.Claim.ClaimedInputRevision != 2 {
				t.Fatalf("schema76 queued Frame stranded: %+v %v", claimed, err)
			}
		})
	}
}

func TestSchema76QueuedStandaloneInputSurvivesUpgrade(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_finish", true: "prepared_inflight"}[prepared], func(t *testing.T) {
			_, repo, stream := schema76QueuedInputUpgrade(t, false, prepared)
			claimed, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "upgraded-owner", TTL: time.Minute})
			if err != nil || !claimed.Claimed || claimed.Claim.StreamUID != stream.UID || claimed.Claim.ClaimedInputRevision != 2 {
				t.Fatalf("schema76 queued standalone stranded: %+v %v", claimed, err)
			}
		})
	}
}
