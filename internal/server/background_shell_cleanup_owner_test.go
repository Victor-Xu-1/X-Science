package server

import (
	"context"
	"errors"
	"testing"
	"time"

	taskstore "x-science/internal/persistence/tasks"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/tools/shellops"
)

const pendingCleanupFixtureError = "private native cleanup diagnostic"

func newPendingCleanupFixture(t *testing.T) (*agentSaveArtifactsFixture, context.Context, backgroundShellOwner) {
	t.Helper()
	fixture := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{
		SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim},
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	owner := fixture.server.currentBackgroundShellOwner(ctx)
	if owner == nil {
		t.Fatal("fixture needs a validated runner claim")
	}
	return fixture, ctx, *owner
}

func registerPendingCleanupFixture(t *testing.T, srv *Server, owner *backgroundShellOwner) (string, *backgroundShellEntry) {
	t.Helper()
	task, err := srv.taskStore.CreateWithOptions(taskstore.CreateOptions{
		Subject: "protected shell", Status: "running", Metadata: map[string]any{"task_type": "shell_command"},
	})
	if err != nil || !srv.admitBackgroundShell() {
		t.Fatalf("could not register shell fixture: %v", err)
	}
	entry := srv.reserveBackgroundShell(task.ID, owner)
	// A valid no-op cleanup handle lets this fixture observe the durable
	// effect of granting a retry without needing another native fault hook.
	srv.attachBackgroundShell(task.ID, entry, &shellops.RunningCommand{})
	srv.settleBackgroundShell(task.ID, entry, backgroundShellSettlement{}, errors.New(pendingCleanupFixtureError))
	return task.ID, entry
}

func TestPendingShellCleanupRejectsForeignOrMissingActor(t *testing.T) {
	fixture, actorCtx, owner := newPendingCleanupFixture(t)
	srv := fixture.server
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		owner *backgroundShellOwner
	}{
		{"other-frame", actorCtx, copyCleanupOwner(owner, func(o *backgroundShellOwner) { o.FrameID = "other-frame" })},
		{"old-root-incarnation", actorCtx, copyCleanupOwner(owner, func(o *backgroundShellOwner) { o.RootFrameIncarnationID = "retired-root" })},
		{"other-branch", actorCtx, copyCleanupOwner(owner, func(o *backgroundShellOwner) { o.BranchID = "other-branch" })},
		{"no-claim", context.Background(), &owner},
		{"unowned-launch", actorCtx, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, entry := registerPendingCleanupFixture(t, srv, tc.owner)
			output, err := srv.executeTaskOutputTool(tc.ctx, "TaskOutput", map[string]any{"task_id": id, "block": true, "timeout": 10})
			payload := mapValue(mapValue(output)["task"])
			if err != nil || mapValue(output)["retrieval_status"] != "timeout" || payload["cleanupPending"] != nil || payload["error"] != nil {
				t.Errorf("foreign cleanup diagnostics exposed: %#v err=%v", output, err)
			}
			result, err := srv.executeTaskStopTool(tc.ctx, "TaskStop", map[string]any{"task_id": id})
			srv.backgroundShellMu.Lock()
			untouched := srv.backgroundShells[id] == entry && entry.pending != nil && entry.stopBy == "" && !entry.recovering
			srv.backgroundShellMu.Unlock()
			if result != nil || err == nil || !untouched {
				t.Errorf("foreign cleanup was retried: result=%#v err=%v untouched=%t", result, err, untouched)
			}
		})
	}
	t.Run("ordinary-durable-task", func(t *testing.T) {
		task, err := srv.taskStore.CreateWithOptions(taskstore.CreateOptions{Subject: "durable task", Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := srv.executeTaskStopTool(context.Background(), "TaskStop", map[string]any{"task_id": task.ID})
		if err != nil || mapValue(result)["task_id"] != task.ID {
			t.Fatalf("legacy durable stop changed: %#v err=%v", result, err)
		}
	})
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.stopAllBackgroundShells(closeCtx); err != nil {
		t.Fatalf("trusted shutdown did not settle retained owners: %v", err)
	}
}

func copyCleanupOwner(owner backgroundShellOwner, change func(*backgroundShellOwner)) *backgroundShellOwner {
	change(&owner)
	return &owner
}

func TestPendingShellCleanupAcceptsNewAttemptWithSameStableOwner(t *testing.T) {
	fixture, staleCtx, owner := newPendingCleanupFixture(t)
	srv := fixture.server
	id, _ := registerPendingCleanupFixture(t, srv, &owner)
	if _, _, _, err := fixture.repo.FinishRunner(staleCtx, transcriptstore.FinishRunnerInput{
		Claim: fixture.claim, ClientMessageID: "cleanup-original-end", Status: "failed",
		PayloadJSON: []byte(`{"status":"failed","detail":"synthetic runner ended while exact cleanup remains pending"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, repeated, err := srv.submitFrameMessage(fixture.store, frameMessageSubmission{
		FrameID: fixture.stream.FrameID, MessageUUID: "cleanup-continuation", ClientMessageID: "cleanup-continuation",
		Text: "Continue cleanup for the same background command.",
	}); err != nil || repeated {
		t.Fatalf("could not admit new input: repeated=%t err=%v", repeated, err)
	}
	stream, found, err := fixture.repo.GetFrameStreamBySession(t.Context(), fixture.stream.OwnerID, fixture.stream.SessionID)
	if err != nil || !found {
		t.Fatalf("missing continued stream: %v", err)
	}
	claimed, err := fixture.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "cleanup-continued-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.Attempt <= fixture.claim.Attempt {
		t.Fatalf("fixture did not get a distinct attempt: %+v err=%v", claimed, err)
	}
	active := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{
		SessionID: stream.SessionID, Attempt: int(claimed.Claim.Attempt),
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim},
	})
	if resolved := srv.currentBackgroundShellOwner(active); resolved == nil || *resolved != owner {
		t.Fatalf("continued caller does not have the original stable owner: %+v", resolved)
	}
	if result, err := srv.executeTaskStopTool(staleCtx, "TaskStop", map[string]any{"task_id": id}); result != nil || err == nil {
		t.Fatalf("retired attempt gained cleanup authority: %#v err=%v", result, err)
	}
	output, err := srv.executeTaskOutputTool(active, "TaskOutput", map[string]any{"task_id": id, "block": true, "timeout": 2000})
	payload := mapValue(mapValue(output)["task"])
	if err != nil || mapValue(output)["retrieval_status"] != "not_ready" || payload["cleanupPending"] != true || payload["error"] != pendingCleanupFixtureError {
		t.Fatalf("continued owner lost cleanup diagnostics: %#v err=%v", output, err)
	}
	stopped, err := srv.executeTaskStopTool(active, "TaskStop", map[string]any{"task_id": id})
	if err != nil || mapValue(stopped)["task_id"] != id {
		t.Fatalf("continued owner could not retry cleanup: %#v err=%v", stopped, err)
	}
}

func TestPendingShellCleanupDeniedReadCannotAcquireLaterRegistryOwner(t *testing.T) {
	fixture, ctx, owner := newPendingCleanupFixture(t)
	srv := fixture.server
	foreign := copyCleanupOwner(owner, func(o *backgroundShellOwner) { o.BranchID = "other-branch" })
	id, entry := registerPendingCleanupFixture(t, srv, foreign)
	task, found, err := srv.taskStore.Get(id)
	if err != nil || !found {
		t.Fatalf("missing retained task: %v", err)
	}
	first := srv.taskOutputState(ctx, task, false)
	if !first.cleanupDenied || first.cleanupErr != nil {
		t.Fatalf("foreign observation was not denied: %+v", first)
	}
	srv.backgroundShellMu.Lock()
	entry.owner = &owner
	srv.backgroundShellMu.Unlock()
	later := srv.taskOutputState(ctx, task, first.cleanupDenied)
	if !later.cleanupDenied || later.cleanupErr != nil {
		t.Fatalf("same wait inherited later registry authority: %+v", later)
	}
	fresh := srv.taskOutputState(ctx, task, false)
	if fresh.cleanupErr == nil || fresh.cleanupDenied {
		t.Fatalf("new wait failed fresh matching owner check: %+v", fresh)
	}
}
