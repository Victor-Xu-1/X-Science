package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func waitForCompletedBookmarker(ctx context.Context, store *workspace.Store) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		frames, err := store.ListFramesForRoot("root")
		if err != nil {
			return err
		}
		for _, frame := range frames {
			if frame.AgentName == "BOOKMARKER" && frame.Status == "completed" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("fixture bookmarker did not durably complete")
		case <-ticker.C:
		}
	}
}

func TestBookmarkerAcceptanceLinearizesWithCheckpointClosure(t *testing.T) {
	for _, mode := range []string{"close-before-submit", "parent-cancel-before-submit", "accepted-before-close", "partial-write-failure"} {
		t.Run(mode, func(t *testing.T) {
			app, store, _ := newBookmarkerLifetimeFixture(t, "http://unused.invalid")
			const answer = "First finding. Second finding."
			if _, err := store.AppendFrameEvent(workspace.FrameEventInput{
				ID: "canonical-answer", FrameID: "root", Type: "assistant_message",
				Payload: map[string]any{"_uuid": "answer-id", "role": "assistant", "content": answer},
			}); err != nil {
				t.Fatal(err)
			}
			parent, cancelParent := context.WithCancel(context.Background())
			t.Cleanup(cancelParent)
			ctx, cancel := context.WithCancel(parent)
			scope := &sessionBookmarkerScope{ctx: ctx, cancel: cancel,
				window: map[int]agentruntime.Message{0: {Role: "assistant", Content: answer}}}
			t.Cleanup(scope.close)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			result := agentruntime.RunResult{FinalMessage: agentruntime.Message{Role: "assistant", Content: answer}}
			scope.accept = func(submission sessionBookmarkerSubmission) error {
				if mode == "accepted-before-close" {
					close(entered)
					<-release
				}
				return app.persistSessionBookmarkerSubmission(sessionstore.Session{ID: "root"}, result, nil, 0, submission, scope.window)
			}
			submission := sessionBookmarkerSubmission{HumanDescription: "Saving verified findings",
				Bookmarks: []sessionBookmarkerBookmark{{MessageIndex: 0, Quote: "First finding.", Label: "Finding"}}}
			if mode == "partial-write-failure" {
				db, err := sql.Open("sqlite", filepath.Join(app.fileRoot, "workspace.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				if _, err := db.Exec(`CREATE TRIGGER reject_second_bookmark BEFORE INSERT ON transcript_annotations
					WHEN NEW.note='reject-write' BEGIN SELECT RAISE(ABORT, 'controlled bookmark persistence failure'); END`); err != nil {
					t.Fatal(err)
				}
				submission.Bookmarks = append(submission.Bookmarks, sessionBookmarkerBookmark{MessageIndex: 0, Quote: "Second finding.", Label: "reject-write"})
			}
			raw, err := json.Marshal(submission)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "close-before-submit":
				scope.close()
			case "parent-cancel-before-submit":
				cancelParent()
			}
			var submitErr error
			if mode == "accepted-before-close" {
				submitted := make(chan error, 1)
				go func() { submitted <- scope.submit(raw) }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("submission did not reach its durable acceptance section")
				}
				closed := make(chan struct{})
				go func() { scope.close(); close(closed) }()
				select {
				case <-closed:
					t.Fatal("checkpoint closure overtook an accepted annotation write")
				case <-time.After(20 * time.Millisecond):
				}
				unblock()
				submitErr = <-submitted
				<-closed
			} else {
				submitErr = scope.submit(raw)
			}
			_, accepted := scope.snapshot()
			annotations, listErr := store.ListTranscriptAnnotations("root")
			if listErr != nil {
				t.Fatal(listErr)
			}
			switch mode {
			case "accepted-before-close":
				if submitErr != nil || !accepted || len(annotations) != 1 || annotations[0].MessageUUID != "answer-id" {
					t.Fatalf("accepted bookmark lost: accepted=%t annotations=%#v err=%v", accepted, annotations, submitErr)
				}
			case "partial-write-failure":
				if submitErr == nil || accepted || scope.acceptanceError() == nil || len(annotations) != 1 || annotations[0].AnchorText != "First finding." {
					t.Fatalf("partial persistence was hidden or rolled back: accepted=%t annotations=%#v err=%v", accepted, annotations, submitErr)
				}
				if err := scope.submit(raw); err == nil {
					t.Fatal("failed acceptance reopened an automatic retry")
				}
			default:
				if !errors.Is(submitErr, context.Canceled) || accepted || len(annotations) != 0 {
					t.Fatalf("cancelled submission wrote a bookmark: accepted=%t annotations=%#v err=%v", accepted, annotations, submitErr)
				}
			}
		})
	}
}

func TestBookmarkerSubmissionChecksOwningLiveClaims(t *testing.T) {
	for _, mode := range []string{"live", "wrong-owner", "wrong-root", "new-input", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			app, _, _ := newBookmarkerLifetimeFixture(t, "http://unused.invalid")
			ctx := context.Background()
			stream, found, err := app.transcriptStore.GetFrameStreamBySession(ctx, "owner", "root")
			if err != nil || !found {
				t.Fatalf("parent stream found=%t err=%v", found, err)
			}
			parent, err := app.transcriptStore.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
				StreamUID: stream.UID, OwnerID: "owner", RunnerID: "parent", TTL: time.Minute,
				ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !parent.Claimed {
				t.Fatalf("parent claim=%#v err=%v", parent, err)
			}
			spec, err := app.sessionCompletionBookmarkerExecutionSpec()
			if err != nil {
				t.Fatal(err)
			}
			frame, child, err := app.beginSessionReviewFrameWithClaim(ctx, sessionstore.Session{ID: "root"}, "test-model", int(parent.Claim.Attempt), 0, spec)
			if err != nil {
				t.Fatal(err)
			}
			sessionID := "root"
			switch mode {
			case "wrong-owner":
				child.OwnerID = "another-owner"
			case "wrong-root":
				sessionID = "another-root"
			case "new-input":
				if _, _, err := app.submitFrameMessage(app.workspaceStore, frameMessageSubmission{
					FrameID: "root", MessageUUID: "new-message", ClientMessageID: "new-client", Text: "A different task.",
				}); err != nil {
					t.Fatal(err)
				}
			case "parent-cancel":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			err = app.validateSessionBookmarkerClaims(ctx, sessionID, frame, child, parent.Claim)
			if (mode == "live") != (err == nil) {
				t.Fatalf("claim boundary %s: %v", mode, err)
			}
		})
	}
}
