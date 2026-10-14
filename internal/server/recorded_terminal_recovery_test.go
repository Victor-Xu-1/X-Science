package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRecordedTerminalDispatchRecoveryPreservesAcceptedComposerInput(t *testing.T) {
	f := newRecordedTerminalFixtureWithLease(t, "completed", 20*time.Millisecond)
	f.appendFollowup(t)
	before := f.presentation(t)
	if wait := time.Until(f.dispatch.LeaseExpiresAt.Add(time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	// This new server has no original runner claim or dispatch token in memory.
	// Its ordinary dispatch loop must reclaim the expired durable dispatch.
	restarted := New(Options{Workspace: f.store, Transcript: f.repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = restarted.Close(ctx)
	})
	result, err := restarted.RunFrameResumeDispatchOnce(f.ctx, FrameResumeDispatchOptions{
		WorkerID: "recovered-terminal-worker", ClaimTTL: time.Minute,
	})
	if err != nil || !result.Claimed || !result.Recovered || result.Attempt != f.dispatch.Attempt+1 ||
		result.ResumeEventID != f.dispatch.ResumeEvent.ID || result.TerminalEvent == nil || result.Status != "completed" {
		t.Fatalf("normal terminal recovery=%+v err=%v", result, err)
	}
	if result.Runner.Claimed {
		t.Fatal("terminal recovery started another runner")
	}
	if after := f.presentation(t); after != before || after[0] != "processing" {
		t.Fatalf("terminal recovery changed new input presentation: before=%v after=%v", before, after)
	}
	var receipts, audits int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND attempt=?`,
		f.stream.UID, f.authority.Claim.Attempt).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("original receipt count=%d err=%v", receipts, err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE id=?`, result.TerminalEvent.ID).
		Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("terminal dispatch audit count=%d err=%v", audits, err)
	}
	next, err := f.repo.ClaimNextRunner(f.ctx, transcriptstore.ClaimNextRunnerInput{RunnerID: "followup-owner", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != f.authority.Claim.ClaimedInputRevision+1 {
		t.Fatalf("accepted composer input lost after recovery: next=%+v err=%v", next, err)
	}
}

func TestRecoveredTerminalReferenceValidatesDurableCoordinates(t *testing.T) {
	for _, mismatch := range []string{"wrong_receipt", "wrong_runner", "new_running_attempt", "different_new_runner", "both_references", "user_json"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newRecordedTerminalFixture(t)
			f.appendFollowup(t)
			before := f.presentation(t)
			input := f.completeInput()
			input.RecoveredRunnerTerminal = &transcriptstore.RecoveredRunnerTerminalReference{
				StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: f.authority.Claim.RunnerID,
				Attempt: f.authority.Claim.Attempt, EventID: f.finish.EventID,
			}
			if mismatch != "both_references" {
				input.RunnerTerminal = nil
			}
			var active transcriptstore.ClaimRunnerResult
			switch mismatch {
			case "wrong_receipt":
				input.RecoveredRunnerTerminal.EventID++
			case "wrong_runner":
				input.RecoveredRunnerTerminal.RunnerID = "another-runner"
			case "new_running_attempt", "different_new_runner":
				var err error
				runnerID := f.authority.Claim.RunnerID
				if mismatch == "different_new_runner" {
					runnerID = "new-owner"
				}
				active, err = f.repo.ClaimRunner(f.ctx, transcriptstore.ClaimRunnerInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: runnerID,
					TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
				})
				if err != nil || !active.Claimed {
					t.Fatalf("legitimate next attempt=%+v err=%v", active, err)
				}
			case "user_json":
				encoded, err := json.Marshal(map[string]any{
					"RunnerTerminal":          map[string]any{"EventID": f.finish.EventID},
					"RecoveredRunnerTerminal": map[string]any{"StreamUID": f.stream.UID, "Attempt": f.authority.Claim.Attempt},
				})
				if err != nil {
					t.Fatal(err)
				}
				var supplied workspace.CompleteCompatibilityFrameResumeDispatchInput
				if err := json.Unmarshal(encoded, &supplied); err != nil ||
					supplied.RunnerTerminal != nil || supplied.RecoveredRunnerTerminal != nil {
					t.Fatalf("JSON supplied internal terminal authority: %+v err=%v", supplied, err)
				}
				input.RecoveredRunnerTerminal = supplied.RecoveredRunnerTerminal
			}
			_, _, completionErr := f.store.CompleteCompatibilityFrameResumeDispatch(input)
			wantDispatch := "claimed"
			if mismatch == "different_new_runner" {
				wantDispatch = "completed"
				if completionErr != nil {
					t.Fatalf("old receipt could not settle its own dispatch: %v", completionErr)
				}
			} else if completionErr == nil {
				t.Fatal("unverified recovered reference was accepted")
			}
			if after := f.presentation(t); after != before {
				t.Fatalf("rejected recovery changed Frame: before=%v after=%v", before, after)
			}
			dispatch, found, err := f.store.GetCompatibilityFrameResumeDispatch(f.dispatch.ResumeEvent.ID)
			if err != nil || !found || dispatch.Status != wantDispatch {
				t.Fatalf("rejected recovery changed dispatch=%+v found=%t err=%v", dispatch, found, err)
			}
			if active.Claimed {
				if _, err := f.repo.ValidateLiveRunnerClaim(f.ctx, active.Claim); err != nil {
					t.Fatalf("rejected recovery affected live attempt: %v", err)
				}
			}
		})
	}
}
