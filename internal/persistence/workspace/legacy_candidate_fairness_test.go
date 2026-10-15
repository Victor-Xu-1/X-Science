package workspace

import (
	"fmt"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestHistoricalControlCandidatesCannotStarveLaterOrdinaryInput(t *testing.T) {
	db, path := openTargetTwentyTwoProviderDB(t)
	applyTranscriptMigration(t, db, 76)
	legacy := transcriptstore.NewRepository(db)
	var last transcriptstore.Stream
	// One historical database with more candidates than the bounded page. The
	// old writer accepts real control responses; it has no admission ledger.
	for index := 0; index < 35; index++ {
		uid := fmt.Sprintf("historical-candidate-%02d", index)
		stream, err := legacy.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: uid, OwnerID: "owner", ExternalID: uid, SessionID: uid, Kind: transcriptstore.StreamKindStandalone, Epoch: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, created, err := appendHistoricalMigrationUserEvent(t, t.Context(), db, legacy, transcriptstore.AppendUserEventInput{StreamUID: uid, OwnerID: "owner", ClientMessageID: "initial", PayloadJSON: []byte(`{"text":"Initial task"}`)}); err != nil || !created {
			t.Fatalf("initial: %t %v", created, err)
		}
		if _, _, err := legacy.ActivateDeliveryRoute(t.Context(), stream.OwnerID, uid, uid); err != nil {
			t.Fatal(err)
		}
		claim, err := legacy.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: uid, OwnerID: "owner", RunnerID: "old-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
		if err != nil || !claim.Claimed {
			t.Fatalf("old claim: %+v %v", claim, err)
		}
		if _, _, _, err := legacy.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "done", Status: "completed", PayloadJSON: []byte(`{"text":"Done"}`)}); err != nil {
			t.Fatal(err)
		}
		staged, err := legacy.StageUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: uid, OwnerID: "owner", ClientMessageID: "historical-control", PayloadJSON: []byte(`{"decision":"allow"}`)})
		if err != nil || !staged.Created {
			t.Fatalf("old control stage: %+v %v", staged, err)
		}
		if err := legacy.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
			if _, err := tx.ExecContext(t.Context(), `UPDATE transcript_events SET event_type='user_input_response' WHERE stream_uid=? AND event_id=? AND event_type='user_message_staged'`, uid, staged.Event.EventID); err != nil {
				return err
			}
			_, err := tx.ExecContext(t.Context(), `UPDATE transcript_streams SET input_revision=input_revision+1,updated_at=? WHERE stream_uid=?`, staged.Event.CreatedAt, uid)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		last = stream
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repo.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{StreamUID: last.UID, OwnerID: last.OwnerID, ClientMessageID: "ordinary-new-input", PayloadJSON: []byte(`{"text":"Compare the existing results."}`)}); err != nil || !created {
		t.Fatalf("current ordinary input: %t %v", created, err)
	}
	first, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "bounded-first-page", TTL: time.Minute})
	if err != nil || first.Claimed {
		t.Fatalf("first page exceeded its bound: %+v %v", first, err)
	}
	// The frame-only pool has an independent cursor and cannot reset the all-
	// channel pool's progress. Store calls reuse the same canonical repository.
	if same, err := store.TranscriptRepository(t.Context()); err != nil || same != repo {
		t.Fatalf("repository cursor was discarded: %p %p %v", repo, same, err)
	}
	if frame, err := repo.ClaimNextFrameRunner(t.Context(), transcriptstore.ClaimNextFrameRunnerInput{RunnerID: "independent-frame-pool", TTL: time.Minute}); err != nil || frame.Claimed {
		t.Fatalf("frame pool: %+v %v", frame, err)
	}
	next, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "bounded-next-page", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Stream.UID != last.UID || next.Claim.ClaimedInputRevision != 3 {
		t.Fatalf("unknown head starved valid input: %+v %v", next, err)
	}
}
