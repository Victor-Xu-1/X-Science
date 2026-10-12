package server

import (
	"context"
	"errors"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
)

func startTaskRunForCancelPropagationTest(t *testing.T, srv *Server) taskruns.Record {
	t.Helper()
	started, err := srv.executeTaskRunTool(map[string]any{
		"action":           "start",
		"objective":        "propagate TaskRun cancellation",
		"success_criteria": []any{"child execution stops when TaskRun is cancelled"},
		"task_graph": explicitTaskRunTestGraph("step-one", "step-two"),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, ok := started.(taskruns.Record)
	if !ok || run.SessionID == "" || run.Status != "running" {
		t.Fatalf("started TaskRun = %#v", started)
	}
	if len(run.ActiveChildren) != 1 {
		t.Fatalf("active children = %#v", run.ActiveChildren)
	}
	return run
}

func claimTaskRunChildRunner(t *testing.T, srv *Server, sessionID, runnerID string) sessionstore.Session {
	t.Helper()
	claimed, claimedOK, err := srv.sessionStore.ClaimRunner(sessionID, runnerID, time.Minute)
	if err != nil || !claimedOK || claimed.Runner == nil {
		t.Fatalf("claim session=%s runner=%s claimed=%t err=%v session=%#v", sessionID, runnerID, claimedOK, err, claimed)
	}
	return claimed
}

func taskRunRunnerMutationInput(session sessionstore.Session, extra map[string]any) map[string]any {
	input := map[string]any{
		"sessionId":     session.ID,
		"runnerId":      session.Runner.RunnerID,
		"runnerAttempt": session.Runner.Attempt,
		"claimToken":    sessionstore.RunnerClaimToken(session.ID, session.Runner),
	}
	for key, value := range extra {
		input[key] = value
	}
	return input
}

func TestTaskRunCancelStopsRegisteredChildExecution(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	session := claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")

	workStarted := make(chan struct{})
	workRelease := make(chan struct{})
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "taskrun-child-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishRun()
	go func() {
		close(workStarted)
		select {
		case <-runCtx.Done():
		case <-workRelease:
		}
	}()

	select {
	case <-workStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled child work did not start")
	}

	cancelled, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID})
	if err != nil {
		t.Fatal(err)
	}
	cancelledRun := cancelled.(taskruns.Record)
	if cancelledRun.Status != "cancelled" || len(cancelledRun.ActiveChildren) != 0 {
		t.Fatalf("cancelled TaskRun = %#v", cancelledRun)
	}

	select {
	case <-runCtx.Done():
		if !errors.Is(context.Cause(runCtx), ErrGenerationStopped) {
			t.Fatalf("child context cause = %v", context.Cause(runCtx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TaskRun cancel did not stop the registered child context")
	}

	heartbeat, err := srv.heartbeatSessionRunner(taskRunRunnerMutationInput(session, map[string]any{"ttlSeconds": 60}))
	if err != nil || heartbeat["renewed"] != true {
		t.Fatalf("heartbeat after cancel = %#v err=%v", heartbeat, err)
	}

	close(workRelease)
}

func TestTaskRunCancelIsIdempotentAndDoesNotStopUnrelatedSession(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "taskrun-child-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishRun()

	otherCtx, _, finishOther, err := srv.registerActiveSessionRun(context.Background(), "other-session", "other-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishOther()

	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("TaskRun child context was not cancelled")
	}
	select {
	case <-otherCtx.Done():
		t.Fatalf("unrelated session was cancelled: %v", context.Cause(otherCtx))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTaskRunCancelDoesNotStopStaleActiveRegistration(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")

	staleCtx, _, finishStale, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "stale-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishStale()

	session, found, err := srv.sessionStore.Get(run.SessionID)
	if err != nil || !found || session.Runner == nil {
		t.Fatalf("session=%#v found=%t err=%v", session, found, err)
	}
	session.Runner.RunnerID = "current-claim-runner"
	if err := srv.sessionStore.Save(session); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-staleCtx.Done():
		t.Fatalf("stale active registration was stopped despite claim mismatch: %v", context.Cause(staleCtx))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTaskRunCancelLateChildFinishDoesNotAdvanceGraph(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	session := claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "taskrun-child-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishRun()

	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("child context was not cancelled")
	}

	input := runnerFinishRecoveryInput(t, srv, run.SessionID, "taskrun-child-runner", "taskrun-late-finish", "completed", "late output", session.Runner.Attempt)
	input["runId"] = run.RunID
	if _, err := srv.finishSessionRunner(input); err != nil {
		t.Fatalf("late finish: %v", err)
	}
	updated, found, err := srv.taskRunStore.Get(run.RunID)
	if err != nil || !found {
		t.Fatalf("get TaskRun found=%t err=%v", found, err)
	}
	if updated.Status != "cancelled" {
		t.Fatalf("late finish changed TaskRun status: %#v", updated)
	}
	if updated.Steps[1].Status != "cancelled" {
		t.Fatalf("next graph step was not left cancelled: %#v", updated.Steps)
	}
}

func TestTaskRunCancelStopsMultipleOwnedChildSessions(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	secondarySessionID := run.SessionID + ":delegate"
	if err := srv.sessionStore.Upsert(sessionstore.Session{ID: secondarySessionID, Title: "delegate", WorkDir: srv.fileRoot}); err != nil {
		t.Fatal(err)
	}
	_, err := srv.taskRunStore.Update(run.RunID, func(record taskruns.Record) (taskruns.Record, error) {
		record.ActiveChildren = append(record.ActiveChildren, taskruns.ActiveChild{
			TaskID: secondarySessionID, StepID: "delegate-step", Executor: "agent",
		})
		return record, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claimTaskRunChildRunner(t, srv, run.SessionID, "primary-runner")
	claimTaskRunChildRunner(t, srv, secondarySessionID, "delegate-runner")

	primaryCtx, _, finishPrimary, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "primary-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishPrimary()
	delegateCtx, _, finishDelegate, err := srv.registerActiveSessionRun(context.Background(), secondarySessionID, "delegate-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishDelegate()

	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}
	for name, childCtx := range map[string]context.Context{"primary": primaryCtx, "delegate": delegateCtx} {
		select {
		case <-childCtx.Done():
			if !errors.Is(context.Cause(childCtx), ErrGenerationStopped) {
				t.Fatalf("%s child cause = %v", name, context.Cause(childCtx))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s child context was not cancelled", name)
		}
	}
}

func TestTaskRunCancelAfterChildAlreadyFinishedIsHarmless(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	session := claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "taskrun-child-runner")
	if err != nil {
		t.Fatal(err)
	}
	finishRun()
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("finished child context should be done")
	}

	input := runnerFinishRecoveryInput(t, srv, run.SessionID, "taskrun-child-runner", "taskrun-finish-before-cancel", "completed", "done", session.Runner.Attempt)
	input["runId"] = run.RunID
	if _, err := srv.finishSessionRunner(input); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.executeTaskRunTool(map[string]any{"action": "cancel", "run_id": run.RunID}); err != nil {
		t.Fatal(err)
	}
}

func TestStopActiveSessionRunControlStillCancelsWithoutTaskRun(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), "control-session", "control-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishRun()
	if stopped, runnerID := srv.stopActiveSessionRun("control-session", "control stop"); !stopped || runnerID != "control-runner" {
		t.Fatalf("stopActiveSessionRun stopped=%t runner=%q", stopped, runnerID)
	}
	select {
	case <-runCtx.Done():
		if !errors.Is(context.Cause(runCtx), ErrGenerationStopped) {
			t.Fatalf("cause = %v", context.Cause(runCtx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active stop control did not cancel context")
	}
}

func TestUncancelledTaskRunChildCanStillComplete(t *testing.T) {
	srv := newRunnerFinishRecoveryFixture(t, "unused", "runner-a", 1)
	run := startTaskRunForCancelPropagationTest(t, srv)
	session := claimTaskRunChildRunner(t, srv, run.SessionID, "taskrun-child-runner")
	runCtx, _, finishRun, err := srv.registerActiveSessionRun(context.Background(), run.SessionID, "taskrun-child-runner")
	if err != nil {
		t.Fatal(err)
	}
	defer finishRun()
	finishRun()

	input := runnerFinishRecoveryInput(t, srv, run.SessionID, "taskrun-child-runner", "taskrun-normal-finish", "completed", "done", session.Runner.Attempt)
	input["runId"] = run.RunID
	if _, err := srv.finishSessionRunner(input); err != nil {
		t.Fatal(err)
	}
	reconciled, err := srv.reconcileTaskRun(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != "waiting_next_step" {
		t.Fatalf("uncancelled completion status = %#v", reconciled)
	}
	select {
	case <-runCtx.Done():
	default:
	}
}
