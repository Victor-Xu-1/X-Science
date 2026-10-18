package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationArchiveOpaqueExtraCannotAuthorizeWork(t *testing.T) {
	store, repo := newConversationArchiveFixture(t)
	frame, err := store.CreateFrame(CreateFrameInput{
		ID: "archive-opaque-review", ProjectID: "archive-project", AgentName: "agent", Status: FrameStatusCompleted, ConversationType: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: "archive-owner", ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.ID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := store.SetFrameRuntimeMetadata(frame.ID, FrameRuntimeMetadata{
		FrameID: frame.ID, ContextData: map[string]any{"web_extra": "opaque-legacy-presentation", "runtime_owned": "retain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConversationPresentation(metadata.ContextData); !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Errorf("opaque extra projected as known active: %v", err)
	}
	if err := transcriptstore.CheckFrameConversationAvailable(context.Background(), store.db, frame.ID); !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Errorf("opaque extra authorized availability: %v", err)
	}
	// Retain the pre-existing Pin error contract instead of changing its old
	// opaque-extra regression assertion to accommodate the new archive reader.
	zero := int64(0)
	if _, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{
		OwnerID: "archive-owner", FrameID: frame.ID, IncarnationID: frame.IncarnationID,
		ExpectedRevision: &zero, Extra: map[string]any{"pinned": true},
	}); !errors.Is(err, ErrConversationPresentationInvalid) {
		t.Errorf("opaque Pin error contract changed: %v", err)
	}
	if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "opaque-review-input", FrameEventID: "opaque-review-frame-input",
		MessageUUID: "opaque-review-input", Text: "Do not infer a missing archive marker from an opaque object",
	}); !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) || created {
		t.Errorf("opaque extra admitted work: created=%t err=%v", created, err)
	}
	current, err := repo.GetStream(context.Background(), stream.UID, stream.OwnerID)
	if err != nil || current.InputRevision != stream.InputRevision {
		t.Fatalf("rejected opaque admission changed input revision: before=%#v after=%#v err=%v", stream, current, err)
	}
}

func TestConversationArchiveCanonicalFinishOutranksUnclaimedProcessingLabel(t *testing.T) {
	for _, finished := range []bool{true, false} {
		t.Run(map[bool]string{true: "canonical-completed-and-consumed", false: "processing-without-terminal-proof"}[finished], func(t *testing.T) {
			store, repo := newConversationArchiveFixture(t)
			frame := conversationArchiveFixtureFrame(t, store, "archive-terminal-review", 1, false, false)
			store.now = time.Now
			if finished {
				claim := archiveFixtureClaim(t, repo, frame)
				_, receipt, created, err := repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
					Claim: claim, ClientMessageID: "archive-review-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed","text":"Work is complete"}`),
				})
				if err != nil || !created || receipt.Status != "completed" || receipt.EventID == 0 {
					t.Fatalf("canonical finish=%#v created=%t err=%v", receipt, created, err)
				}
				stream, err := repo.GetStream(context.Background(), claim.StreamUID, claim.OwnerID)
				if err != nil || stream.InputRevision != stream.ConsumedInputRevision || stream.InputRevision != claim.ClaimedInputRevision {
					t.Fatalf("terminal input was not consumed: %#v err=%v", stream, err)
				}
			}
			// This existing legacy reservation API changes only the display Frame
			// to processing. There is no accepted next input or runner claim. The
			// archive-vs-final-admission fence must decide the later input; no SQL
			// status/counter/lease rewriting or invented finish receipt is used.
			if activated, err := store.ActivateCompletedCompatibilityFrameRequest(frame.ID); err != nil || !activated {
				t.Fatalf("unclaimed legacy reservation=%t err=%v", activated, err)
			}
			before, _, err := store.GetFrame(frame.ID)
			if err != nil || before.Status != FrameStatusProcessing {
				t.Fatalf("raw label=%#v err=%v", before, err)
			}
			result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(frame, true, 7))
			if finished {
				if err != nil || !result.Changed {
					t.Fatalf("canonical completion was blocked by the raw label: result=%#v err=%v", result, err)
				}
			} else if !errors.Is(err, ErrConversationArchiveBusy) {
				t.Fatalf("unknown processing label was treated as terminal: %v", err)
			}
			after, _, err := store.GetFrame(frame.ID)
			if err != nil || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("archive repaired a label by rewriting activity: before=%#v after=%#v err=%v", before, after, err)
			}
		})
	}
}
