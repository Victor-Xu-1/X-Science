package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestCompletionRecoveryRestoresCanonicalDeliverableIntent(t *testing.T) {
	for _, test := range []struct {
		name, text string
		missing    bool
		continued  bool
	}{
		{name: "explicit file", text: "Save the result as report.csv", missing: true},
		{name: "continued explicit file", text: "Save the result as report.csv", missing: true, continued: true},
		{name: "no file obligation", text: "Explain the result briefly in the conversation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, session, authority := completionIntentRecoveryFixture(t, test.text, false, test.continued)
			intent, found, err := server.transcriptStore.GetActiveFrameTaskIntent(context.Background(), authority.Stream.UID, authority.Stream.OwnerID)
			if err != nil || !found {
				t.Fatalf("intent=%#v found=%t err=%v", intent, found, err)
			}
			candidate, found, err := server.loadTranscriptCompletionRecoveryCandidate(context.Background(), authority)
			if err != nil || !found || candidate.SourceRun == nil {
				t.Fatalf("candidate=%#v found=%t err=%v", candidate, found, err)
			}
			run := candidate.SourceRun
			if run.TaskIntent != test.text || run.TaskIntentID != intent.ID || run.TaskIntentRevision != intent.Revision ||
				run.Transcript.Claim.ClaimedInputRevision != authority.Claim.ClaimedInputRevision {
				t.Fatalf("recovered task authority=%#v claim=%#v intent=%#v", run, run.Transcript.Claim, intent)
			}
			_, err = server.validateRecoveredTranscriptCompletionCandidate(context.Background(), session, candidate)
			if !test.missing {
				if err != nil {
					t.Fatalf("ordinary prose completion gained a file obligation: %v", err)
				}
				return
			}
			var integrity *sessionRunnerReferenceIntegrityError
			if !errors.As(err, &integrity) || !strings.Contains(strings.Join(integrity.MissingRequiredDeliverables, ";"), "artifact report.csv") {
				t.Fatalf("missing explicit output was accepted during recovery: %v", err)
			}
			before, err := server.transcriptStore.GetProjectionSnapshot(context.Background(), authority.Stream.UID, authority.Stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			status, detail, _, handled, err := consumeCompletionRecoveryFixture(context.Background(), server, session,
				&sessionRunnerChatRun{SessionID: session.ID, Transcript: authority})
			if err != nil || handled || status != "" || detail != "" {
				t.Fatalf("missing deliverable did not enter ordinary checkpoint repair: status=%q detail=%q handled=%t err=%v", status, detail, handled, err)
			}
			after, err := server.transcriptStore.GetProjectionSnapshot(context.Background(), authority.Stream.UID, authority.Stream.OwnerID)
			if err != nil || after != before {
				t.Fatalf("repair delegation published a replacement answer: before=%#v after=%#v err=%v", before, after, err)
			}
			retained, found, err := server.loadTranscriptCompletionRecoveryCandidate(context.Background(), authority)
			if err != nil || !found || retained.Content != candidate.Content {
				t.Fatalf("repair delegation lost original candidate=%#v found=%t err=%v", retained, found, err)
			}
		})
	}
}

func TestCompletionRecoveryDoesNotBindLaterInputToOldClaim(t *testing.T) {
	server, _, authority := completionIntentRecoveryFixture(t, "Save the result as report.csv", false)
	if _, _, err := server.submitFrameMessage(server.workspaceStore, frameMessageSubmission{
		FrameID: authority.Stream.FrameID, MessageUUID: "later-task", ClientMessageID: "later-task", Text: "Just answer the next question in chat",
	}); err != nil {
		t.Fatal(err)
	}
	// Both supplied claim and Stream are deliberately stale. Matching the old
	// pair alone is insufficient: the durable stream already contains new input.
	candidate, found, err := server.loadTranscriptCompletionRecoveryCandidate(context.Background(), authority)
	if err != nil || found {
		t.Fatalf("later input inherited stale candidate=%#v found=%t err=%v", candidate, found, err)
	}
	intent, found, err := server.transcriptStore.GetActiveFrameTaskIntent(context.Background(), authority.Stream.UID, authority.Stream.OwnerID)
	if err != nil || !found || intent.Text != "Just answer the next question in chat" {
		t.Fatalf("recovery mutated the new task intent=%#v found=%t err=%v", intent, found, err)
	}
}

func TestCompletionRecoveryPreservesStandaloneIntent(t *testing.T) {
	server, session, authority := completionIntentRecoveryFixture(t, "Explain the outcome in chat", true)
	candidate, found, err := server.loadTranscriptCompletionRecoveryCandidate(context.Background(), authority)
	if err != nil || !found || candidate.SourceRun.TaskIntent != "Explain the outcome in chat" ||
		candidate.SourceRun.TaskIntentRevision != authority.Claim.ClaimedInputRevision || candidate.SourceRun.TaskIntentID == "" {
		t.Fatalf("standalone candidate=%#v found=%t err=%v", candidate, found, err)
	}
	if _, err := server.validateRecoveredTranscriptCompletionCandidate(context.Background(), session, candidate); err != nil {
		t.Fatalf("standalone completion recovery was disabled: %v", err)
	}
}

func completionIntentRecoveryFixture(t *testing.T, text string, standalone bool, continued ...bool) (*Server, sessionstore.Session, *transcriptRunnerAuthority) {
	t.Helper()
	ctx := context.Background()
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "intent-recovery-project", "intent-recovery-frame")
	server := &Server{workspaceStore: store, transcriptStore: repo}
	var stream transcriptstore.Stream
	if standalone {
		var err error
		stream, err = repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
			UID: "intent-recovery-standalone", OwnerID: "local", ExternalID: "intent-recovery-standalone",
			SessionID: "standalone-session", Kind: transcriptstore.StreamKindStandalone, Epoch: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"text": text})
		if _, _, err := repo.AppendUserEvent(ctx, transcriptstore.AppendUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "intent-recovery-user", PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
			FrameID: "intent-recovery-frame", MessageUUID: "intent-recovery-user", ClientMessageID: "intent-recovery-user", Text: text,
		}); err != nil {
			t.Fatal(err)
		}
		var found bool
		var err error
		stream, found, err = repo.GetFrameStreamBySession(ctx, "local", "intent-recovery-frame")
		if err != nil || !found {
			t.Fatalf("stream found=%t err=%v", found, err)
		}
		if len(continued) > 0 && continued[0] {
			if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
				FrameID: stream.FrameID, MessageUUID: "intent-recovery-continue", ClientMessageID: "intent-recovery-continue", Text: "continue",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	claim, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "intent-recovery-first", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	checkpoint, _, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim.Claim, ClientMessageID: "intent-recovery-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting,
		// Machine checkpoint text is not user intent, even if its payload
		// happens to contain user-shaped fields.
		Resumable: true, PayloadJSON: []byte(`{"status":"running","role":"user","text":"Save unrelated-machine-file.csv"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, _ := json.Marshal(map[string]any{"text": "The result is ready.", "delta_index": 1,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, err := repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
		Claim: claim.Claim, ClientMessageID: "intent-recovery-delta", Type: "content_delta", Source: transcriptstore.EventSourcePayload, PayloadJSON: delta,
	}); err != nil {
		t.Fatal(err)
	}
	terminal, _ := json.Marshal(map[string]any{"status": "failed",
		"detail":            "runner completion reference integrity failed (unsupported_citations=1)",
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")})
	if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: claim.Claim, ClientMessageID: "intent-recovery-finished", Status: "failed", PayloadJSON: terminal,
	}); err != nil {
		t.Fatal(err)
	}
	// Model the authorized new-attempt checkpoint recovery. The tested methods
	// are read-only and never use this synthetic claim to append or settle events.
	authority := &transcriptRunnerAuthority{Stream: stream, Claim: transcriptstore.RunnerClaim{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, Attempt: claim.Claim.Attempt + 1,
		ClaimedInputRevision: claim.Claim.ClaimedInputRevision, ResumeSource: transcriptstore.ResumeSourceCheckpoint,
		ResumeCheckpoint: checkpoint.Sequence, ResumeCheckpointAttempt: claim.Claim.Attempt,
	}}
	return server, sessionstore.Session{ID: stream.SessionID, Project: &sessionstore.Project{ID: "intent-recovery-project"}}, authority
}
