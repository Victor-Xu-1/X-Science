package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
)

func TestTaskRunCancellationPrivateFinishReplayAfterPendingInputAndReclaim(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	session, claim := taskRunCancellationClaim(t, srv, run.SessionID)
	_, active := bindTaskRunCancellationFixture(t, srv, session)
	if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": session.ID, "role": "user", "clientMessageId": "replay-pending", "message": map[string]any{"text": "Pending ordinary input"}}); err != nil {
		t.Fatal(err)
	}
	cancelTaskRunFixture(t, srv, run)
	active.settlement.Lock()
	receipt := taskRunCancellationSettlementLocked(active, claim)
	active.settlement.Unlock()
	input := map[string]any{"sessionId": session.ID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "status": "cancelled", "message": "propagated cancellation", "clientMessageId": "cancel-replay"}
	first, err := srv.finishSessionRunner(input, receipt)
	if err != nil {
		t.Fatal(err)
	}
	original := first["event"].(*eventjournal.Entry)
	if stringValue(original.Message["taskRunClaimSHA256"]) == "" {
		t.Fatal("canonical propagated cancellation lacks exact claim proof")
	}
	pending, _, _ := srv.sessionStore.Get(session.ID)
	if pending.Runner != nil || pending.LastRole != "user" {
		t.Fatalf("pending input hidden: %#v", pending)
	}
	for _, reclaim := range []bool{false, true} {
		if reclaim {
			if _, ok, err := srv.sessionStore.ClaimNextRunner("new-input-owner", time.Minute); err != nil || !ok {
				t.Fatalf("reclaim=%t err=%v", ok, err)
			}
		}
		before, _, _ := srv.sessionStore.Get(session.ID)
		graphBefore, _, _ := srv.taskRunStore.Get(run.RunID)
		entriesBefore, _ := srv.eventJournal.ReadAll(session.ID)
		replay, err := srv.finishSessionRunner(input, receipt)
		if err != nil || replay["event"].(*eventjournal.Entry).EventID != original.EventID || boolValue(replay["delivered"], true) {
			t.Fatalf("immutable replay=%#v err=%v", replay, err)
		}
		after, _, _ := srv.sessionStore.Get(session.ID)
		graphAfter, _, _ := srv.taskRunStore.Get(run.RunID)
		entriesAfter, _ := srv.eventJournal.ReadAll(session.ID)
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(graphBefore, graphAfter) || !reflect.DeepEqual(entriesBefore, entriesAfter) {
			t.Fatal("read-only retry mutated later claim/graph/history")
		}
		if _, err := srv.finishSessionRunner(input); !errors.Is(err, sessionstore.ErrRunnerClaimStale) {
			t.Fatalf("public stale claim gained private replay: %v", err)
		}
		conflict := copyTaskRunCancellationInput(input)
		conflict["message"] = "changed terminal payload"
		if _, err := srv.finishSessionRunner(conflict, receipt); !errors.Is(err, eventjournal.ErrIdempotencyConflict) {
			t.Fatalf("conflicting replay accepted: %v", err)
		}
		unknown := copyTaskRunCancellationInput(input)
		unknown["clientMessageId"] = "new-stale-finish"
		if _, err := srv.finishSessionRunner(unknown, receipt); !errors.Is(err, sessionstore.ErrRunnerClaimStale) {
			t.Fatalf("new stale finish accepted: %v", err)
		}
		otherReceipt := *receipt
		otherReceipt.instance = "different-instance"
		if _, err := srv.finishSessionRunner(input, &otherReceipt); !errors.Is(err, eventjournal.ErrIdempotencyConflict) {
			t.Fatalf("instance conflict accepted: %v", err)
		}
		changedToken := copyTaskRunCancellationInput(input)
		changedToken["claimToken"] = "another-claim-token"
		otherReceipt = *receipt
		otherReceipt.claim.ClaimToken = "another-claim-token"
		if _, err := srv.finishSessionRunner(changedToken, &otherReceipt); !errors.Is(err, eventjournal.ErrIdempotencyConflict) {
			t.Fatalf("claim proof conflict accepted: %v", err)
		}
	}
}

func TestTaskRunCancellationAdmissionReadFailureReleasesOnlyAcquiredClaim(t *testing.T) {
	for _, mode := range []string{"missing", "read_error", "reserved"} {
		t.Run(mode, func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "")
			options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: "admission-error", Endpoint: "http://controlled.invalid/never", Model: "controlled", DisableSkillDiscovery: true, DisableMCPDiscovery: true}
			var reserved *sessionstore.RunnerMutationClaim
			if mode == "reserved" {
				session, claimed, err := srv.sessionStore.ClaimRunner(run.SessionID, options.RunnerID, time.Minute)
				if err != nil || !claimed {
					t.Fatal(err)
				}
				claim := sessionstore.RunnerClaimFromSession(session)
				reserved = &claim
			}
			journalFile := filepath.Join(srv.fileRoot, "session-events", url.QueryEscape(run.SessionID)+".jsonl")
			if mode == "read_error" {
				if err := os.Remove(journalFile); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(journalFile, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				session, _, _ := srv.sessionStore.Get(run.SessionID)
				session.LastUserEventID = 9999
				if err := srv.sessionStore.Save(session); err != nil {
					t.Fatal(err)
				}
			}
			result, err := srv.runSessionRunnerChatOnce(context.Background(), options, reserved)
			if err == nil || !result.Claimed || srv.hasActiveSessionRun(run.SessionID) {
				t.Fatalf("failed admission result=%+v err=%v", result, err)
			}
			current, _, _ := srv.sessionStore.Get(run.SessionID)
			if mode == "reserved" {
				if _, err := srv.sessionStore.ValidateRunnerClaim(*reserved, true); err != nil {
					t.Fatalf("borrowed claim released: %v", err)
				}
			} else if current.Runner != nil {
				t.Fatalf("newly acquired claim stranded: %#v", current.Runner)
			}
		})
	}
}

func TestTaskRunCancellationUnknownPublicReceiptIsReportedWithoutGuessing(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	session, claim := taskRunCancellationClaim(t, srv, run.SessionID)
	if session.Runner.InputReceipt != nil {
		t.Fatal("direct store claim fabricated public receipt")
	}
	finished, err := srv.finishSessionRunner(map[string]any{"sessionId": run.SessionID, "runnerId": claim.RunnerID, "runnerAttempt": claim.Attempt, "claimToken": claim.ClaimToken, "status": "completed", "clientMessageId": "unknown-input-finish", "taskRunInstanceId": "caller-instance"})
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(mapValue(finished["taskRunProjection"])["state"]) != "unconfirmed" || stringValue(finished["event"].(*eventjournal.Entry).Message["taskRunInstanceId"]) != "" {
		t.Fatalf("unknown receipt guessed/hidden: %#v", finished)
	}
	graph, err := srv.reconcileTaskRun(run.RunID)
	if err != nil || graph.Status != "running" {
		t.Fatalf("unknown finish settled graph: %#v err=%v", graph, err)
	}
}

func copyTaskRunCancellationInput(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func TestTaskRunCancellationPublicAdmissionReadFailureReleasesUnreturnedClaim(t *testing.T) {
	for _, entryPoint := range []string{"next", "pick", "claim"} {
		t.Run(entryPoint, func(t *testing.T) {
			srv := newTaskRunCancellationServer(t, nil)
			run := startTaskRunCancellationFixture(t, srv, "")
			srv.eventJournal = eventjournal.NewEventJournalWithLimits(srv.fileRoot, eventjournal.Limits{MaxEntryBytes: 16})
			input := map[string]any{"sessionId": run.SessionID, "runnerId": "unreturned-public", "limit": 20}
			var err error
			switch entryPoint {
			case "next":
				_, err = srv.nextSessionRunner(input)
			case "pick":
				_, err = srv.pickSessionRunner(input)
			case "claim":
				_, err = srv.claimSessionRunner(input)
			}
			if !errors.Is(err, eventjournal.ErrEntrySizeLimit) {
				t.Fatalf("public read error=%v", err)
			}
			session, _, readErr := srv.sessionStore.Get(run.SessionID)
			if readErr != nil || session.Runner != nil {
				t.Fatalf("unreturned claim stranded: %#v err=%v", session.Runner, readErr)
			}
		})
	}
}

func TestTaskRunCancellationBoundedCommandReplayDoesNotUseLaterQueuedTaskRun(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	const sessionID = "taskrun:bounded-input"
	if err := srv.sessionStore.Upsert(sessionstore.Session{ID: sessionID}); err != nil {
		t.Fatal(err)
	}
	normal, err := srv.appendSessionToolEvent(map[string]any{"sessionId": sessionID, "role": "user", "clientMessageId": "old-normal", "message": map[string]any{"text": "Actual first-page normal input"}})
	if err != nil {
		t.Fatal(err)
	}
	run := startTaskRunCancellationFixture(t, srv, "bounded-input")
	cancelTaskRunFixture(t, srv, run)
	calls := 0
	result, err := srv.runSessionRunnerCommandOnce(context.Background(), SessionRunnerCommandOptions{RunnerID: "bounded-command", Command: "synthetic", ReplayLimit: 1}, func(_ context.Context, _ SessionRunnerCommandOptions, input sessionRunnerCommandInput) (sessionRunnerCommandOutput, error) {
		calls++
		if len(input.Entries) != 1 || input.Entries[0].EventID != normal.EventID {
			t.Fatalf("wrong replay=%#v", input.Entries)
		}
		return sessionRunnerCommandOutput{Status: "completed", AssistantMessage: "Normal bounded input answer"}, nil
	})
	if err != nil || calls != 1 || result.Status != "completed" {
		t.Fatalf("queued event outside actual replay stopped normal work: %+v calls=%d err=%v", result, calls, err)
	}
	finish := taskRunFinishEntry(t, srv, result)
	if stringValue(finish.Message["taskRunInstanceId"]) != "" {
		t.Fatalf("bounded normal replay acquired later TaskRun instance: %#v", finish)
	}
}

func TestTaskRunCancellationPostcommitLedgerFailureStillSignalsExactExecution(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	session, _ := taskRunCancellationClaim(t, srv, run.SessionID)
	ctx, _ := bindTaskRunCancellationFixture(t, srv, session)
	if err := srv.runtimeStore.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID})
	if err == nil || !errors.Is(context.Cause(ctx), ErrGenerationStopped) {
		t.Fatalf("ledger error suppressed cancellation: value=%#v err=%v cause=%v", value, err, context.Cause(ctx))
	}
	persisted, _, readErr := srv.taskRunStore.Get(run.RunID)
	if readErr != nil || persisted.Status != "cancelled" {
		t.Fatalf("postcommit graph=%#v err=%v", persisted, readErr)
	}
}

func TestTaskRunCancellationAlreadyFinishedRunnerKeepsDurableSuccess(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	run := startTaskRunCancellationFixture(t, srv, "")
	result, err := srv.runSessionRunnerCommandOnce(context.Background(), SessionRunnerCommandOptions{RunnerID: "completion-winner", Command: "synthetic"}, func(context.Context, SessionRunnerCommandOptions, sessionRunnerCommandInput) (sessionRunnerCommandOutput, error) {
		return sessionRunnerCommandOutput{Status: "completed", AssistantMessage: "Successful controlled execution"}, nil
	})
	if err != nil || result.Status != "completed" {
		t.Fatalf("completion control=%+v err=%v", result, err)
	}
	before, _, _ := srv.sessionStore.Get(run.SessionID)
	finishBefore := taskRunFinishEntry(t, srv, result)
	cancelTaskRunFixture(t, srv, run)
	after, _, _ := srv.sessionStore.Get(run.SessionID)
	finishAfter := taskRunFinishEntry(t, srv, result)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(finishBefore, finishAfter) || after.Runner.Status != "completed" {
		t.Fatal("later graph cancellation rewrote settled successful execution")
	}
}

func TestTaskRunCancellationOrdinaryCancelledCommandParentKeepsExistingAuditPath(t *testing.T) {
	srv := newTaskRunCancellationServer(t, nil)
	const sessionID = "ordinary-cancelled-command"
	if err := srv.sessionStore.Upsert(sessionstore.Session{ID: sessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": sessionID, "role": "user", "clientMessageId": "ordinary-input", "message": map[string]any{"text": "Ordinary session input"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrGenerationStopped)
	calls := 0
	result, err := srv.runSessionRunnerCommandOnce(ctx, SessionRunnerCommandOptions{RunnerID: "ordinary-parent", Command: "synthetic"}, func(ctx context.Context, _ SessionRunnerCommandOptions, _ sessionRunnerCommandInput) (sessionRunnerCommandOutput, error) {
		calls++
		if !errors.Is(context.Cause(ctx), ErrGenerationStopped) {
			t.Fatal("ordinary executor lost existing cancelled context")
		}
		return sessionRunnerCommandOutput{}, context.Cause(ctx)
	})
	if err != nil || calls != 1 || result.Status != "cancelled" || result.CheckpointEventID == 0 {
		t.Fatalf("ordinary cancelled audit path changed: %+v calls=%d err=%v", result, calls, err)
	}
	entries, _ := srv.eventJournal.ReadAll(sessionID)
	checkpoints := 0
	for _, entry := range entries {
		if stringValue(entry.Message["type"]) == "runner_checkpoint" {
			checkpoints++
		}
	}
	if checkpoints != 1 {
		t.Fatalf("ordinary existing checkpoint count=%d", checkpoints)
	}
}

func TestTaskRunCancellationAlreadyCancelledCommandParentUsesOnlyReplayAdmission(t *testing.T) {
	for _, plain := range []bool{false, true} {
		for _, mode := range []string{"queued_taskrun", "ordinary_input", "replacement"} {
			t.Run(fmt.Sprintf("plain_%t_%s", plain, mode), func(t *testing.T) {
				srv := newTaskRunCancellationServer(t, nil)
				run := startTaskRunCancellationFixture(t, srv, "")
				if mode == "ordinary_input" {
					if _, err := srv.appendSessionToolEvent(map[string]any{"sessionId": run.SessionID, "role": "user", "clientMessageId": "early-command-normal", "message": map[string]any{"text": "Later ordinary input"}}); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancelCause(context.Background())
				cause := error(ErrGenerationStopped)
				if plain {
					cause = context.Canceled
				}
				cancel(cause)
				calls := 0
				var replacement taskruns.Record
				result, err := srv.runSessionRunnerCommandOnce(ctx, SessionRunnerCommandOptions{RunnerID: "cancelled-command-parent", Command: "synthetic"}, func(ctx context.Context, _ SessionRunnerCommandOptions, _ sessionRunnerCommandInput) (sessionRunnerCommandOutput, error) {
					calls++
					if mode == "replacement" {
						replacement = startTaskRunCancellationFixture(t, srv, run.RunID)
					}
					return sessionRunnerCommandOutput{}, context.Cause(ctx)
				})
				want := "cancelled"
				if plain {
					want = "failed"
				}
				if err != nil || calls != 1 || result.Status != want || result.CheckpointEventID == 0 {
					t.Fatalf("parent cancelled existing audit path=%+v calls=%d err=%v", result, calls, err)
				}
				finish := taskRunFinishEntry(t, srv, result)
				instance := stringValue(finish.Message["taskRunInstanceId"])
				if mode == "ordinary_input" && instance != "" || mode != "ordinary_input" && instance != taskruns.ExecutionInstance(run) {
					t.Fatalf("early command input admission=%#v", finish)
				}
				if mode == "replacement" && instance == taskruns.ExecutionInstance(replacement) {
					t.Fatal("early producer guessed replacement identity")
				}
				graph, err := srv.reconcileTaskRun(run.RunID)
				graphWant := want
				if mode != "queued_taskrun" {
					graphWant = "running"
				}
				if err != nil || graph.Status != graphWant {
					t.Fatalf("early producer graph=%#v want=%s err=%v", graph, graphWant, err)
				}
				session, _, _ := srv.sessionStore.Get(run.SessionID)
				if session.Runner == nil || session.Runner.Status != want {
					t.Fatal("admission-only parent cancellation enabled pending reopening")
				}
			})
		}
	}
}
