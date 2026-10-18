package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationArchiveResumeSelectionSkipsUnavailableRoots(t *testing.T) {
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
			store, repo := newConversationArchiveFixture(t)
			base := time.Now().UTC()
			streams := make([]transcriptstore.Stream, 0, 2)
			resumes := make([]ResumeCompatibilityFrameResult, 0, 2)
			for index, id := range []string{"unavailable-resume-root", "healthy-resume-root"} {
				now := base.Add(time.Duration(index) * time.Second)
				store.now = func() time.Time { return now }
				frame, err := store.CreateFrame(CreateFrameInput{
					ID: id, ProjectID: "archive-project", AgentName: "agent", Status: FrameStatusProcessing, ConversationType: "agent",
				})
				if err != nil {
					t.Fatal(err)
				}
				stream, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
					UID: "frame:" + id, OwnerID: "archive-owner", ExternalID: id, SessionID: id,
					Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: id, FrameID: id, Epoch: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				resume, err := store.CreateAutoResumeDispatch(id, id, frame.ProjectID, frame.AgentName, "explicit_user_continue")
				if err != nil || resume.Event == nil {
					t.Fatalf("real dispatch=%#v err=%v", resume, err)
				}
				streams, resumes = append(streams, stream), append(resumes, resume)
			}
			bad := streams[0]
			// Seed the first presentation only after genuine dispatch creation.
			// No lease, dispatch payload, frame status, or canonical counter is rewritten.
			prior, found, err := store.GetFrameRuntimeMetadata(bad.FrameID)
			if err != nil {
				t.Fatal(err)
			}
			if _, alreadySeeded := prior.ContextData["web_extra"]; found && alreadySeeded {
				t.Fatal("fixture already has protected presentation")
			}
			beforeMetadata, err := store.SetFrameRuntimeMetadata(bad.FrameID, FrameRuntimeMetadata{
				FrameID: bad.FrameID, ContextData: map[string]any{"web_extra": test.extra, "runtime_owned": "preserve"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := transcriptstore.CheckFrameConversationAvailable(context.Background(), store.db, bad.FrameID); !errors.Is(err, test.want) {
				t.Fatalf("fixture did not persist intended availability: %v metadata=%#v", err, beforeMetadata)
			}
			beforeDispatch, found, err := store.GetCompatibilityFrameResumeDispatch(resumes[0].Event.ID)
			if err != nil || !found {
				t.Fatalf("bad dispatch unavailable: %v", err)
			}
			if !resumes[0].Event.CreatedAt.Before(resumes[1].Event.CreatedAt) {
				t.Fatal("bad dispatch must be the oldest actual candidate")
			}
			claimed, found, claimErr := store.ClaimNextCompatibilityFrameResumeDispatch("recovery-worker", time.Minute)
			if claimErr != nil || !found || claimed.FrameID != streams[1].FrameID {
				t.Errorf("unavailable root starved healthy global resume: claim=%#v found=%t err=%v", claimed, found, claimErr)
			}
			afterDispatch, found, err := store.GetCompatibilityFrameResumeDispatch(resumes[0].Event.ID)
			if err != nil || !found || !reflect.DeepEqual(beforeDispatch, afterDispatch) {
				t.Errorf("bad dispatch changed: before=%#v after=%#v err=%v", beforeDispatch, afterDispatch, err)
			}
			if _, err := store.CreateAutoResumeDispatch(bad.FrameID, bad.FrameID, bad.ProjectID, "agent", "explicit_user_continue"); !errors.Is(err, test.want) {
				t.Errorf("direct resume silently bypassed archive guard: %v", err)
			}
			if _, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: bad.UID, OwnerID: bad.OwnerID, ClientMessageID: "rejected-input", FrameEventID: "rejected-frame-input",
				MessageUUID: "rejected-input", Text: "An ordinary new task",
			}); !errors.Is(err, test.want) || created {
				t.Errorf("direct input bypassed typed guard: created=%t err=%v", created, err)
			}
			afterStream, err := repo.GetStream(context.Background(), bad.UID, bad.OwnerID)
			if err != nil || afterStream.InputRevision != bad.InputRevision || afterStream.NextPublication != bad.NextPublication {
				t.Errorf("bad canonical coordinates changed: %#v err=%v", afterStream, err)
			}
			afterMetadata, found, err := store.GetFrameRuntimeMetadata(bad.FrameID)
			if err != nil || !found || !reflect.DeepEqual(beforeMetadata, afterMetadata) {
				t.Errorf("bad metadata changed: %#v err=%v", afterMetadata, err)
			}
		})
	}
}

func TestConversationArchiveResumeSelectionPropagatesStorageFailure(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimNextCompatibilityFrameResumeDispatch("recovery-worker", time.Minute); err == nil || claimed ||
		errors.Is(err, transcriptstore.ErrConversationArchived) || errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Fatalf("real storage error treated as a skippable archive marker: claimed=%t err=%v", claimed, err)
	}
}
