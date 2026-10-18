package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationArchiveAppendRaceHasExactlyOneWinner(t *testing.T) {
	store, repo := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-race", 1, false, false)
	store.now = time.Now
	start := make(chan struct{})
	archiveDone := make(chan error, 1)
	inputDone := make(chan error, 1)
	go func() {
		<-start
		result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7))
		if err == nil && !result.Changed {
			err = errors.New("first archive unexpectedly reported a noop")
		}
		archiveDone <- err
	}()
	go func() {
		<-start
		_, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
			StreamUID: "frame:" + frame.ID, OwnerID: "archive-owner", ClientMessageID: "concurrent-input",
			FrameEventID: "concurrent-frame-input", MessageUUID: "concurrent-input", Text: "Concurrent user request",
		})
		if err == nil && !created {
			err = errors.New("first input unexpectedly replayed an existing event")
		}
		inputDone <- err
	}()
	close(start)
	archiveErr, inputErr := <-archiveDone, <-inputDone
	if (archiveErr == nil) == (inputErr == nil) {
		t.Fatalf("expected one commit winner, archive=%v input=%v", archiveErr, inputErr)
	}
	stream, err := repo.GetStream(context.Background(), "frame:"+frame.ID, "archive-owner")
	if err != nil {
		t.Fatal(err)
	}
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	archivedAt, err := transcriptstore.ConversationArchivedAt(metadata.ContextData)
	if err != nil {
		t.Fatal(err)
	}
	if archiveErr == nil {
		if !errors.Is(inputErr, transcriptstore.ErrConversationArchived) || archivedAt == 0 || stream.InputRevision != 0 {
			t.Fatalf("archived winner retained an admitted input: archived=%d stream=%#v err=%v", archivedAt, stream, inputErr)
		}
	} else if !errors.Is(archiveErr, ErrConversationArchiveBusy) || archivedAt != 0 || stream.InputRevision != 1 {
		t.Fatalf("input winner was hidden: archived=%d stream=%#v archiveErr=%v", archivedAt, stream, archiveErr)
	}
}

func TestConversationArchiveRejectsRecreatedIdentityAndStaleRuntimeCannotUnarchive(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	frame := conversationArchiveFixtureFrame(t, store, "archive-aba", 1, true, false)
	store.now = time.Now
	oldMetadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldRequest := conversationArchiveUpdate(frame, true, 7)
	if _, err := store.UpdateConversationArchive(context.Background(), oldRequest); err != nil {
		t.Fatal(err)
	}
	oldMetadata.ContextData["runtime_owned"] = "updated by runtime"
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, oldMetadata); err != nil {
		t.Fatal(err)
	}
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archivedAt, err := transcriptstore.ConversationArchivedAt(metadata.ContextData); err != nil || archivedAt == 0 ||
		metadata.ContextData["web_extra"].(map[string]any)[presentationRevisionKey] != float64(8) {
		t.Fatalf("stale runtime erased archive: %#v err=%v", metadata, err)
	}
	if _, err := store.DeleteCompatibilityFrameTree(frame.ID, "archive-owner", frame.IncarnationID); err != nil {
		t.Fatal(err)
	}
	replacement := conversationArchiveFixtureFrame(t, store, frame.ID, 2, false, false)
	if replacement.IncarnationID == frame.IncarnationID {
		t.Fatal("replacement reused the old incarnation")
	}
	if _, err := store.UpdateConversationArchive(context.Background(), oldRequest); !errors.Is(err, ErrConversationPresentationStale) {
		t.Fatalf("old archive receipt mutated recreated identity: %v", err)
	}
}
