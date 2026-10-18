package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestConversationArchiveQueueRecoverySkipsUnavailableRoots(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra any
		want  error
	}{
		{"opaque", "opaque-presentation", transcriptstore.ErrConversationArchiveInvalid},
		{"bad-marker", map[string]any{"archived_at": "unknown"}, transcriptstore.ErrConversationArchiveInvalid},
		{"archived", map[string]any{"archived_at": int64(1)}, transcriptstore.ErrConversationArchived},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, repo, db := newTranscriptWebFixture(t)
			bad, badQueue := archiveQueueRecoveryFixture(t, store, repo, "unavailable-queue-root")
			good, goodQueue := archiveQueueRecoveryFixture(t, store, repo, "healthy-queue-root")
			app := New(Options{FileRoot: t.TempDir(), Workspace: store, Transcript: repo})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = app.Close(ctx)
			})
			beforeMetadata, found, err := store.GetFrameRuntimeMetadata(bad.FrameID)
			if err != nil || !found {
				t.Fatalf("actual terminal metadata missing: %v", err)
			}
			if _, seeded := beforeMetadata.ContextData["web_extra"]; seeded {
				t.Fatal("fixture already has a protected presentation object")
			}
			beforeMetadata.ContextData["web_extra"] = test.extra
			beforeMetadata, err = store.SetFrameRuntimeMetadata(bad.FrameID, beforeMetadata)
			if err != nil {
				t.Fatal(err)
			}
			if err := transcriptstore.CheckFrameConversationAvailable(context.Background(), db, bad.FrameID); !errors.Is(err, test.want) {
				t.Fatalf("fixture did not persist intended marker: %v metadata=%#v", err, beforeMetadata)
			}
			ids, err := store.CompatibilityQueuedFrameIDs()
			if err != nil || len(ids) != 2 || ids[0] != bad.FrameID {
				t.Fatalf("bad frame is not the oldest queue candidate: %v err=%v", ids, err)
			}
			beforeStream, err := repo.GetStream(context.Background(), bad.UID, bad.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			advanced, recoverErr := app.recoverCompatibilityQueuedMessages()
			if recoverErr != nil || advanced != 1 {
				t.Errorf("unavailable root starved healthy queue: advanced=%d err=%v", advanced, recoverErr)
			}
			afterBadQueue, found, err := store.GetCompatibilityMessageIntent(badQueue.IntentID)
			if err != nil || !found || !reflect.DeepEqual(badQueue, afterBadQueue) {
				t.Errorf("rejected queue claim was not released exactly: before=%#v after=%#v err=%v", badQueue, afterBadQueue, err)
			}
			if queuedEvent, found, err := store.GetFrameEventByID(badQueue.QueuedEventID); err != nil || !found || queuedEvent.Type != "queued_message" {
				t.Errorf("rejected queued user event was lost: event=%#v found=%t err=%v", queuedEvent, found, err)
			}
			afterGoodQueue, found, err := store.GetCompatibilityMessageIntent(goodQueue.IntentID)
			if err != nil || !found || afterGoodQueue.State != "drained" {
				t.Errorf("healthy queued input not delivered: %#v found=%t err=%v", afterGoodQueue, found, err)
			}
			accepted, found, err := repo.GetEventByClientMessageID(context.Background(), good.UID, good.OwnerID, goodQueue.IntentID)
			if err != nil || !found || accepted.Type != "user_message" {
				t.Errorf("healthy delivery lacks canonical user event: %#v found=%t err=%v", accepted, found, err)
			}
			if _, _, err := app.submitFrameMessage(store, frameMessageSubmission{
				FrameID: bad.FrameID, ClientMessageID: "direct-rejected-input", MessageUUID: "direct-rejected-input", Text: "An ordinary explicit follow-up",
			}); !errors.Is(err, test.want) {
				t.Errorf("direct input lost typed refusal: %v", err)
			}
			afterStream, err := repo.GetStream(context.Background(), bad.UID, bad.OwnerID)
			if err != nil || afterStream.InputRevision != beforeStream.InputRevision || afterStream.ConsumedInputRevision != beforeStream.ConsumedInputRevision || afterStream.NextPublication != beforeStream.NextPublication {
				t.Errorf("rejected canonical input changed: before=%#v after=%#v err=%v", beforeStream, afterStream, err)
			}
			afterMetadata, found, err := store.GetFrameRuntimeMetadata(bad.FrameID)
			if err != nil || !found || !reflect.DeepEqual(beforeMetadata, afterMetadata) {
				t.Errorf("unknown/archive metadata was modified: %#v err=%v", afterMetadata, err)
			}
			afterFrame, found, err := store.GetFrame(bad.FrameID)
			if err != nil || !found || afterFrame.Status != "completed" {
				t.Errorf("rejected queue reactivated terminal frame: %#v err=%v", afterFrame, err)
			}
		})
	}
}

func archiveQueueRecoveryFixture(t *testing.T, store *workspace.Store, repo *transcriptstore.Repository, id string) (transcriptstore.Stream, workspace.CompatibilityMessageIntent) {
	t.Helper()
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-" + id, UserID: "local", Name: id}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: id, ProjectID: "project-" + id, AgentName: "xscience", Status: "processing", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + id, OwnerID: "local", ExternalID: id, SessionID: id, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: frame.ProjectID, RootFrameID: id, FrameID: id, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "initial-" + id, FrameEventID: "initial-event-" + id,
		MessageUUID: "initial-" + id, Text: "Initial task", Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("initial input created=%t err=%v", created, err)
	}
	claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "runner-" + id, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("real runner claim=%#v err=%v", claimed, err)
	}
	if _, _, created, err := repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
		Claim: claimed.Claim, ClientMessageID: "finished-" + id, Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`), Destinations: []string{"ws"},
	}); err != nil || !created {
		t.Fatalf("real terminal created=%t err=%v", created, err)
	}
	current, found, err := store.GetFrame(id)
	if err != nil || !found || current.Status != "completed" {
		t.Fatalf("terminal frame was not projected: %#v err=%v", current, err)
	}
	text := "Please examine a different ordinary question."
	queued, _, replayed, err := store.QueueCompatibilityMessage(id, "queued-"+id,
		map[string]any{"request": text}, map[string]any{"text": text, "inputData": map[string]any{"request": text}})
	if err != nil || replayed || queued.State != "queued" {
		t.Fatalf("real queued input=%#v replayed=%t err=%v", queued, replayed, err)
	}
	persisted, found, err := store.GetCompatibilityMessageIntent(queued.IntentID)
	if err != nil || !found {
		t.Fatalf("durable queued input missing: found=%t err=%v", found, err)
	}
	return stream, persisted
}

func TestConversationArchiveQueueRecoveryPropagatesStorageFailure(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	app := New(Options{FileRoot: t.TempDir(), Workspace: store, Transcript: repo})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = app.Close(ctx)
	})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if advanced, err := app.recoverCompatibilityQueuedMessages(); err == nil || advanced != 0 ||
		errors.Is(err, transcriptstore.ErrConversationArchived) || errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Fatalf("real storage failure hidden by marker isolation: advanced=%d err=%v", advanced, err)
	}
}
