package server

import (
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRecordedRunnerFinishRetryPreservesNewInputAndOwner(t *testing.T) {
	for _, claimNewRunner := range []bool{false, true} {
		name := "pending_input"
		if claimNewRunner {
			name = "new_runner"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecordedTerminalFixture(t)
			f.appendFollowup(t)
			var next transcriptstore.ClaimNextRunnerResult
			if claimNewRunner {
				var err error
				next, err = f.repo.ClaimNextRunner(f.ctx, transcriptstore.ClaimNextRunnerInput{RunnerID: "new-owner", TTL: time.Minute})
				if err != nil || !next.Claimed {
					t.Fatalf("new owner=%+v err=%v", next, err)
				}
			}
			before := f.presentation(t)
			replayed, err := f.server.finishTranscriptRunner(f.ctx, f.authority, "completed", recordedTerminalMessage)
			if err != nil || replayed.EventID != f.finish.EventID {
				t.Fatalf("original finish replay=%+v err=%v", replayed, err)
			}
			if after := f.presentation(t); after != before || after[0] != "processing" {
				t.Fatalf("recorded finish changed new Frame: before=%v after=%v", before, after)
			}
			if _, _, err := f.store.CompleteCompatibilityFrameResumeDispatch(f.completeInput()); err != nil {
				t.Fatalf("old dispatch settlement: %v", err)
			}
			if after := f.presentation(t); after != before {
				t.Fatalf("old dispatch changed new Frame: before=%v after=%v", before, after)
			}
			if claimNewRunner {
				if _, err := f.repo.ValidateLiveRunnerClaim(f.ctx, next.Claim); err != nil {
					t.Fatalf("recorded finish invalidated the new owner: %v", err)
				}
			}
		})
	}
}

func TestRecordedDispatchRetryPreservesNewFramePresentation(t *testing.T) {
	f := newRecordedTerminalFixture(t)
	input := f.completeInput()
	terminal, repeated, err := f.store.CompleteCompatibilityFrameResumeDispatch(input)
	if err != nil || repeated || terminal.ID == "" {
		t.Fatalf("initial dispatch completion=%+v repeated=%t err=%v", terminal, repeated, err)
	}
	f.appendFollowup(t)
	before := f.presentation(t)
	replay, repeated, err := f.store.CompleteCompatibilityFrameResumeDispatch(input)
	if err != nil || !repeated || replay.ID != terminal.ID {
		t.Fatalf("dispatch replay=%+v repeated=%t err=%v", replay, repeated, err)
	}
	if after := f.presentation(t); after != before || after[0] != "processing" {
		t.Fatalf("old dispatch replay changed presentation: before=%v after=%v", before, after)
	}
	var audits int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE id=?`, terminal.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("terminal audit count=%d err=%v", audits, err)
	}
}

func TestRecordedDispatchCleanupPreservesExplicitCancellation(t *testing.T) {
	for _, treeCancellation := range []bool{false, true} {
		name := "terminal_only_cleanup"
		if treeCancellation {
			name = "cancelled_dispatch_retry"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecordedTerminalFixture(t)
			f.appendFollowup(t)
			if treeCancellation {
				if _, err := f.store.CancelCompatibilityFrameTree(f.stream.FrameID, "user_cancelled"); err != nil {
					t.Fatal(err)
				}
			} else if _, _, err := f.store.CancelFrameWithTranscript(f.ctx, f.stream.FrameID); err != nil {
				t.Fatal(err)
			}
			before := f.presentation(t)
			pending, found, err := f.repo.GetFramePendingInputState(f.ctx, f.stream.OwnerID, f.stream.FrameID)
			if err != nil || !found || pending.InputRevision <= pending.ConsumedInputRevision {
				t.Fatalf("cancellation fixture lacks pending input: %+v found=%t err=%v", pending, found, err)
			}
			input := f.completeInput()
			input.RunnerTerminal = nil
			input.Status = "cancelled"
			terminal, repeated, err := f.store.CompleteCompatibilityFrameResumeDispatch(input)
			if err != nil || terminal.ID == "" || repeated != treeCancellation {
				t.Fatalf("cancelled dispatch close=%+v repeated=%t err=%v", terminal, repeated, err)
			}
			if after := f.presentation(t); before != after || after[0] != "cancelled" {
				t.Fatalf("cleanup changed cancellation: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRecordedDispatchRejectsUnverifiedTerminalReferences(t *testing.T) {
	for _, field := range []string{"dispatch_token", "runner_token", "owner", "stream", "receipt", "details_only"} {
		t.Run(field, func(t *testing.T) {
			f := newRecordedTerminalFixture(t)
			f.appendFollowup(t)
			before := f.presentation(t)
			input := f.completeInput()
			switch field {
			case "dispatch_token":
				input.ClaimToken += "wrong"
			case "runner_token":
				input.RunnerTerminal.Claim.ClaimToken += "wrong"
			case "owner":
				input.RunnerTerminal.Claim.OwnerID = "another-owner"
			case "stream":
				input.RunnerTerminal.Claim.StreamUID = "another-stream"
			case "receipt":
				input.RunnerTerminal.EventID++
			case "details_only":
				input.RunnerTerminal = nil
				input.Details = map[string]any{"runnerId": f.authority.Claim.RunnerID, "runnerAttempt": f.authority.Claim.Attempt, "finishEventId": f.finish.EventID}
			}
			if _, _, err := f.store.CompleteCompatibilityFrameResumeDispatch(input); err == nil {
				t.Fatal("unverified terminal reference was accepted")
			}
			if after := f.presentation(t); after != before {
				t.Fatalf("rejected completion changed Frame: before=%v after=%v", before, after)
			}
			dispatch, found, err := f.store.GetCompatibilityFrameResumeDispatch(f.dispatch.ResumeEvent.ID)
			if err != nil || !found || dispatch.Status != "claimed" {
				t.Fatalf("rejected completion changed dispatch: %+v found=%t err=%v", dispatch, found, err)
			}
		})
	}
}

func TestRecordedRunnerFinishRetryPreservesActiveBranch(t *testing.T) {
	f := newRecordedTerminalFixture(t)
	branch, err := f.repo.GetBranchState(f.ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := f.repo.ForkFrameUserMessageBranch(f.ctx, transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		SourceBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
		ClientMutationID: "f9d5554e-25e8-4a45-8b2f-cb72bb531bed", SourceClientMessageID: "recorded-input", SourceMessageIndex: 0,
		ReplacementText: "Review the revised evidence.", Destinations: []string{"ws"},
	})
	if err != nil || !fork.Created {
		t.Fatalf("branch continuation=%+v err=%v", fork, err)
	}
	before := f.presentation(t)
	if _, err := f.server.finishTranscriptRunner(f.ctx, f.authority, "completed", recordedTerminalMessage); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.CompleteCompatibilityFrameResumeDispatch(f.completeInput()); err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.GetBranchState(f.ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil || current.ActiveBranchID != fork.BranchID || current.Generation != fork.Generation {
		t.Fatalf("old receipt changed active branch=%+v err=%v", current, err)
	}
	if after := f.presentation(t); before != after || after[0] != "processing" {
		t.Fatalf("old receipt terminalized new branch: before=%v after=%v", before, after)
	}
}

func TestRecordedTerminalRetryPreservesSameRevisionContinue(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			f := newRecordedTerminalFixtureWithStatus(t, status)
			resumed, err := f.store.ResumeCompatibilityFrameConversation(f.stream.FrameID, workspace.ResumeCompatibilityFrameInput{})
			if err != nil || resumed.Event == nil || resumed.Event.ID == f.dispatch.ResumeEvent.ID {
				t.Fatalf("normal continuation=%+v err=%v", resumed, err)
			}
			stream, err := f.repo.GetStream(f.ctx, f.stream.UID, f.stream.OwnerID)
			if err != nil || stream.InputRevision != f.authority.Claim.ClaimedInputRevision {
				t.Fatalf("continuation changed input revision: %+v err=%v", stream, err)
			}
			var latestAttempt int64
			if err := f.db.QueryRow(`SELECT MAX(attempt) FROM transcript_runner_attempts WHERE stream_uid=?`, f.stream.UID).
				Scan(&latestAttempt); err != nil || latestAttempt != f.authority.Claim.Attempt {
				t.Fatalf("continuation unexpectedly claimed a runner: attempt=%d err=%v", latestAttempt, err)
			}
			before := f.presentation(t)
			replayed, err := f.server.finishTranscriptRunner(f.ctx, f.authority, status, recordedTerminalMessage)
			if err != nil || replayed.EventID != f.finish.EventID {
				t.Fatalf("recorded finish=%+v err=%v", replayed, err)
			}
			terminal, repeated, err := f.store.CompleteCompatibilityFrameResumeDispatch(f.completeInput())
			if err != nil || repeated || terminal.ID == "" {
				t.Fatalf("old dispatch settlement=%+v repeated=%t err=%v", terminal, repeated, err)
			}
			if after := f.presentation(t); after != before || after[0] != "processing" {
				t.Fatalf("old terminal overwrote Continue: before=%v after=%v", before, after)
			}
			next, found, err := f.store.GetCompatibilityFrameResumeDispatch(resumed.Event.ID)
			if err != nil || !found || next.Status != "registered" {
				t.Fatalf("old settlement changed new dispatch=%+v found=%t err=%v", next, found, err)
			}
		})
	}
}
