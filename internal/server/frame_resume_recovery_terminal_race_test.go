package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRecoveryConditionRechecksKernelTerminalAcrossPark(t *testing.T) {
	for _, completionBeforePark := range []bool{true, false} {
		name := "completion-after-park"
		if completionBeforePark {
			name = "completion-before-park"
		}
		t.Run(name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			if _, err := f.store.CreateAutoResumeDispatch(
				f.stream.SessionID, f.stream.SessionID, f.stream.ProjectID, "OPERON", "initial_task",
			); err != nil {
				t.Fatal(err)
			}
			dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("terminal-race-worker", time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim dispatch: claimed=%t err=%v", claimed, err)
			}
			var claimSequence int64
			for _, audit := range dispatch.AuditEvents {
				if audit.Type == "frame_resume_dispatch_claimed" {
					claimSequence = audit.Sequence
				}
			}
			if claimSequence <= 0 {
				t.Fatal("dispatch claim has no durable causal sequence")
			}
			operation := startRecoveryParkKernelOperation(t, f)
			interrupted, err := f.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
				Claim: f.claim, ClientMessageID: "terminal-race-no-progress",
				ReasonCode: sessionRunnerToolRoundNoProgressExhaustedReasonCode,
				Resumable:  true, AutoResume: false,
				RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
				Destinations:             []string{transcriptWebDestination},
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interrupt unchanged route: created=%t err=%v", interrupted.Created, err)
			}

			// Exercise the actual repository method ordering, not a fabricated
			// dispatcher callback or a production-only test hook. The claim and
			// requeue below are the same methods used by the dispatcher.
			var terminal workspace.FinishKernelLocalOperationResult
			complete := func() {
				terminal = finishRecoveryParkKernelOperation(t, f, operation)
				if terminal.Event.Sequence <= claimSequence {
					t.Fatalf("terminal event predates the owned dispatch claim: claim=%d terminal=%d", claimSequence, terminal.Event.Sequence)
				}
				if err := f.server.wakeFrameResumeDispatchAfterKernelTransition(ctx, terminal.Operation); err != nil {
					t.Fatal(err)
				}
			}
			if completionBeforePark {
				complete()
				current, found, err := f.store.GetCompatibilityFrameResumeDispatch(dispatch.ResumeEvent.ID)
				if err != nil || !found || current.Status != "claimed" || current.Attempt != dispatch.Attempt {
					t.Fatalf("completion changed an owned dispatch: found=%t dispatch=%#v err=%v", found, current, err)
				}
			}
			if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
				ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken,
				ReasonCode: sessionRunnerToolRoundNoProgressExhaustedReasonCode, RunnerAttempt: int(f.claim.Attempt),
				CheckpointEventID:        interrupted.Event.EventID,
				WaitingFor:               workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition,
				RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
			}); err != nil {
				t.Fatal(err)
			}
			if !completionBeforePark {
				complete()
			}
			materialized, found, err := f.store.GetKernelToolResultMaterialization(ctx, operation.OperationID)
			if err != nil || !found || !json.Valid(materialized.TerminalResultJSON) ||
				terminal.Operation.State != workspace.KernelLocalOperationStateCompleted {
				t.Fatalf("terminal evidence missing: found=%t terminal=%#v err=%v", found, terminal.Operation, err)
			}
			unread, err := f.store.CountUnreadNotifications(ctx, operation.FrameID, operation.RootFrameID, operation.OwnerUserID)
			if err != nil || unread != 1 {
				t.Fatalf("background completion notification missing: unread=%d err=%v", unread, err)
			}

			// This is the ordinary periodic recovery consumer, invoked directly
			// without sleeping, changing clocks, submitting new input or replaying
			// the completed computation. The already-committed completion must not
			// depend on whether its first wake preceded the waiting commit.
			f.server.autoResumeInterruptedFrames(ctx)
			current, found, err := f.store.GetCompatibilityFrameResumeDispatch(dispatch.ResumeEvent.ID)
			if err != nil || !found || current.Status != "registered" || current.Attempt != dispatch.Attempt {
				t.Fatalf("recovery replaced the original dispatch: found=%t dispatch=%#v err=%v", found, current, err)
			}
			if current.WaitingFor != "" || !current.NotBefore.IsZero() {
				t.Fatalf("committed background result lost its recovery wake: waiting=%q dispatch=%s operation=%s terminal=%s notification=%s",
					current.WaitingFor, current.ResumeEvent.ID, terminal.Operation.OperationID, terminal.Operation.State, terminal.Notification.ID)
			}
			next, reclaimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("terminal-race-next-worker", time.Minute)
			if err != nil || !reclaimed || next.ResumeEvent.ID != dispatch.ResumeEvent.ID || next.Attempt != dispatch.Attempt+1 {
				t.Fatalf("same logical task was not eligible after completion: claimed=%t dispatch=%#v err=%v", reclaimed, next, err)
			}
			var executions int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM execution_log WHERE frame_id=?`, f.stream.FrameID).Scan(&executions); err != nil {
				t.Fatal(err)
			}
			if executions != 1 {
				t.Fatalf("completed computation was duplicated: execution records=%d", executions)
			}
		})
	}
}

func TestRecoveryConditionKernelCompletionPreservesUnrelatedWaits(t *testing.T) {
	for _, scenario := range []string{
		"no-terminal", "acknowledged-result", "stale-wait-reference", "model-selection",
		"new-input", "whole-task-cancelled", "next-claim-old-terminal",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			dispatch := claimRecoveryParkDispatch(t, f, "guard-worker")
			operation := startRecoveryParkKernelOperation(t, f)
			interrupted, err := f.repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
				Claim: f.claim, ClientMessageID: "guard-no-progress", ReasonCode: sessionRunnerToolRoundNoProgressExhaustedReasonCode,
				Resumable: true, AutoResume: false, RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
				Destinations: []string{transcriptWebDestination},
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interrupt unchanged route: created=%t err=%v", interrupted.Created, err)
			}
			if scenario != "no-terminal" {
				finishRecoveryParkKernelOperation(t, f, operation)
			}
			if scenario == "acknowledged-result" {
				ackRecoveryParkKernelResult(t, f, operation)
			}
			waitingFor := workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition
			if scenario == "model-selection" {
				waitingFor = workspace.CompatibilityFrameResumeDispatchWaitModelSelection
			}
			parked := requeueRecoveryParkDispatch(t, f, dispatch, interrupted, waitingFor)
			wakeCount := 0
			switch scenario {
			case "next-claim-old-terminal":
				event, changed, err := f.store.WakeRecoveryConditionAfterKernelCompletion(ctx, parked.ResumeEvent.ID, parked.WaitRef)
				if err != nil || !changed || event.ID == "" {
					t.Fatalf("first relevant completion did not wake: changed=%t event=%#v err=%v", changed, event, err)
				}
				wakeCount = 1
				if _, changed, err := f.store.WakeRecoveryConditionAfterKernelCompletion(ctx, parked.ResumeEvent.ID, parked.WaitRef); err != nil || changed {
					t.Fatalf("same completion/wait retry was not idempotent: changed=%t err=%v", changed, err)
				}
				next, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("guard-next-worker", time.Minute)
				if err != nil || !claimed || next.ResumeEvent.ID != dispatch.ResumeEvent.ID || next.Attempt != dispatch.Attempt+1 {
					t.Fatalf("claim same dispatch after wake: claimed=%t next=%#v err=%v", claimed, next, err)
				}
				parked = requeueRecoveryParkDispatch(t, f, next, interrupted, waitingFor)
			case "new-input":
				if _, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "guard-new-input",
					FrameEventID: "guard-new-input-event", MessageUUID: "guard-new-input-message", Text: "Use this new input instead.",
				}); err != nil || !created {
					t.Fatalf("append superseding input: created=%t err=%v", created, err)
				}
			case "whole-task-cancelled":
				if _, err := f.store.CancelCompatibilityFrameTreeWithTranscript(ctx, f.stream.FrameID, "user_cancelled"); err != nil {
					t.Fatal(err)
				}
			}
			before, found, err := f.store.GetCompatibilityFrameResumeDispatch(parked.ResumeEvent.ID)
			if err != nil || !found {
				t.Fatalf("read protected wait: found=%t err=%v", found, err)
			}
			beforePayload, err := json.Marshal(before.ResumeEvent.Payload)
			if err != nil {
				t.Fatal(err)
			}
			unread, err := f.store.CountUnreadNotifications(ctx, operation.FrameID, operation.RootFrameID, operation.OwnerUserID)
			if err != nil {
				t.Fatal(err)
			}
			expectedWaitRef := before.WaitRef
			if scenario == "stale-wait-reference" {
				expectedWaitRef = "not-the-current-wait"
			}
			if _, changed, err := f.store.WakeRecoveryConditionAfterKernelCompletion(ctx, before.ResumeEvent.ID, expectedWaitRef); err != nil || changed {
				t.Fatalf("unrelated state acquired execution authority: changed=%t err=%v", changed, err)
			}
			after, found, err := f.store.GetCompatibilityFrameResumeDispatch(before.ResumeEvent.ID)
			if err != nil || !found {
				t.Fatalf("read retained protected wait: found=%t err=%v", found, err)
			}
			afterPayload, err := json.Marshal(after.ResumeEvent.Payload)
			if err != nil || string(afterPayload) != string(beforePayload) {
				t.Fatalf("rejected completion wake changed the wait: before=%s after=%s err=%v", beforePayload, afterPayload, err)
			}
			remaining, err := f.store.CountUnreadNotifications(ctx, operation.FrameID, operation.RootFrameID, operation.OwnerUserID)
			if err != nil || remaining != unread {
				t.Fatalf("wake consumed an execution result: before=%d after=%d err=%v", unread, remaining, err)
			}
			assertFrameResumeDispatchEvents(t, f.store, f.stream.SessionID, map[string]int{"frame_resume_dispatch_woken": wakeCount})
		})
	}
}

func TestRecoveryConditionScanIsolatesInvalidArchiveBeforeHealthyCompletion(t *testing.T) {
	store, repo, db := newTranscriptWebFixture(t)
	app := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	base := &agentSaveArtifactsFixture{server: app, store: store, repo: repo, db: db}
	ctx := context.Background()
	type waitingRoot struct {
		fixture  *agentSaveArtifactsFixture
		dispatch workspace.CompatibilityFrameResumeDispatch
	}
	roots := make([]waitingRoot, 0, 2)
	for _, id := range []string{"recovery-root-first", "recovery-root-second"} {
		f := newRecoveryParkRoot(t, base, id)
		roots = append(roots, waitingRoot{fixture: f, dispatch: claimRecoveryParkDispatch(t, f, "archive-worker-"+id)})
	}
	// Use the actual queue ordering rather than forge event IDs or repeatedly
	// generate UUIDs. Only the selected healthy root starts an operation, so
	// the fixture's immutable execution ID is not reused across two roots.
	if roots[0].dispatch.ResumeEvent.ID > roots[1].dispatch.ResumeEvent.ID {
		roots[0], roots[1] = roots[1], roots[0]
	}
	bad, good := roots[0], roots[1]
	operation := startRecoveryParkKernelOperation(t, good.fixture)
	for index := range roots {
		root := &roots[index]
		interrupted, err := repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
			Claim: root.fixture.claim, ClientMessageID: "archive-no-progress-" + root.fixture.stream.FrameID,
			ReasonCode: sessionRunnerToolRoundNoProgressExhaustedReasonCode,
			Resumable:  true, AutoResume: false, RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
			Destinations: []string{transcriptWebDestination},
		})
		if err != nil || !interrupted.Created {
			t.Fatalf("interrupt root: created=%t err=%v", interrupted.Created, err)
		}
		if index == 1 {
			finishRecoveryParkKernelOperation(t, good.fixture, operation)
		}
		root.dispatch = requeueRecoveryParkDispatch(t, root.fixture, root.dispatch, interrupted,
			workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition)
	}
	bad, good = roots[0], roots[1]
	metadata, found, err := store.GetFrameRuntimeMetadata(bad.fixture.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("bad root metadata missing: found=%t err=%v", found, err)
	}
	if _, present := metadata.ContextData["web_extra"]; present {
		t.Fatal("fixture already established a protected archive presentation")
	}
	if metadata.ContextData == nil {
		metadata.ContextData = map[string]any{}
	}
	metadata.ContextData["web_extra"] = "unavailable-archive-state"
	if _, err := store.SetFrameRuntimeMetadata(bad.fixture.stream.FrameID, metadata); err != nil {
		t.Fatal(err)
	}
	if err := transcriptstore.CheckFrameConversationAvailable(ctx, db, bad.fixture.stream.FrameID); !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Fatalf("normal metadata API did not persist the typed invalid marker: %v", err)
	}
	page, err := store.ListRecoveryConditionWaitDispatches(ctx, "", 100)
	if err != nil || len(page) != 2 || page[0].ResumeEvent.ID != bad.dispatch.ResumeEvent.ID || page[1].ResumeEvent.ID != good.dispatch.ResumeEvent.ID {
		t.Fatalf("invalid root is not before the healthy root in the actual recovery page: page=%#v err=%v", page, err)
	}
	if _, changed, err := store.WakeRecoveryConditionAfterKernelCompletion(ctx, bad.dispatch.ResumeEvent.ID, bad.dispatch.WaitRef); changed || !errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
		t.Fatalf("direct storage helper lost its typed refusal: changed=%t err=%v", changed, err)
	}
	badPayload, err := json.Marshal(bad.dispatch.ResumeEvent.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.wakeChangedFrameRecoveryWaits(ctx); err != nil {
		t.Fatalf("unavailable archive root blocked a healthy completion in the same page: %v", err)
	}
	afterBad, found, err := store.GetCompatibilityFrameResumeDispatch(bad.dispatch.ResumeEvent.ID)
	if err != nil || !found {
		t.Fatalf("invalid root wait was lost: found=%t err=%v", found, err)
	}
	afterBadPayload, err := json.Marshal(afterBad.ResumeEvent.Payload)
	if err != nil || string(afterBadPayload) != string(badPayload) {
		t.Fatalf("invalid root wait was modified: before=%s after=%s err=%v", badPayload, afterBadPayload, err)
	}
	afterGood, found, err := store.GetCompatibilityFrameResumeDispatch(good.dispatch.ResumeEvent.ID)
	if err != nil || !found || afterGood.WaitingFor != "" || afterGood.Attempt != good.dispatch.Attempt {
		t.Fatalf("healthy root completion was starved: found=%t wait=%#v err=%v", found, afterGood, err)
	}
	assertFrameResumeDispatchEvents(t, store, bad.fixture.stream.SessionID, map[string]int{"frame_resume_dispatch_woken": 0})
	assertFrameResumeDispatchEvents(t, store, good.fixture.stream.SessionID, map[string]int{"frame_resume_dispatch_woken": 1})
}

func newRecoveryParkRoot(t *testing.T, base *agentSaveArtifactsFixture, id string) *agentSaveArtifactsFixture {
	t.Helper()
	ctx := context.Background()
	const owner = "local"
	if _, err := base.store.CreateProject(workspace.CreateProjectInput{ID: "project-" + id, UserID: owner, Name: id}); err != nil {
		t.Fatal(err)
	}
	frame, err := base.store.CreateFrame(workspace.CreateFrameInput{ID: id, ProjectID: "project-" + id, AgentName: "OPERON", Status: "processing", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.store.SetFrameRuntimeMetadata(frame.ID, workspace.FrameRuntimeMetadata{FrameID: frame.ID, ContextData: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	stream, err := base.repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "frame:" + id, OwnerID: owner, ExternalID: id, SessionID: id, Kind: transcriptstore.StreamKindFrameRef,
		ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := base.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: owner, ClientMessageID: "input-" + id,
		FrameEventID: "input-event-" + id, MessageUUID: "input-message-" + id, Text: "Inspect the retained work.",
	}); err != nil || !created {
		t.Fatalf("append root input: created=%t err=%v", created, err)
	}
	claimed, err := base.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: owner, RunnerID: "runner-" + id, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim root runner: claimed=%t err=%v", claimed.Claimed, err)
	}
	return &agentSaveArtifactsFixture{server: base.server, store: base.store, repo: base.repo, db: base.db, stream: stream, claim: claimed.Claim}
}

func claimRecoveryParkDispatch(t *testing.T, f *agentSaveArtifactsFixture, worker string) workspace.CompatibilityFrameResumeDispatch {
	t.Helper()
	if _, err := f.store.CreateAutoResumeDispatch(f.stream.SessionID, f.stream.SessionID, f.stream.ProjectID, "OPERON", "initial_task"); err != nil {
		t.Fatal(err)
	}
	dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch(worker, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim dispatch: claimed=%t err=%v", claimed, err)
	}
	return dispatch
}

func requeueRecoveryParkDispatch(t *testing.T, f *agentSaveArtifactsFixture, dispatch workspace.CompatibilityFrameResumeDispatch, interrupted transcriptstore.InterruptRunnerResult, waitingFor string) workspace.CompatibilityFrameResumeDispatch {
	t.Helper()
	if _, _, err := f.store.RequeueCompatibilityFrameResumeDispatch(workspace.RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken,
		ReasonCode: sessionRunnerToolRoundNoProgressExhaustedReasonCode, RunnerAttempt: int(f.claim.Attempt),
		CheckpointEventID: interrupted.Event.EventID, WaitingFor: waitingFor, RecoveryContractRevision: sessionRunnerRecoveryContractRevision,
	}); err != nil {
		t.Fatal(err)
	}
	parked, found, err := f.store.GetCompatibilityFrameResumeDispatch(dispatch.ResumeEvent.ID)
	if err != nil || !found || parked.WaitingFor != waitingFor {
		t.Fatalf("park exact dispatch: found=%t parked=%#v err=%v", found, parked, err)
	}
	return parked
}

func ackRecoveryParkKernelResult(t *testing.T, f *agentSaveArtifactsFixture, operation workspace.KernelLocalOperation) {
	t.Helper()
	ctx := context.Background()
	const claimToken = "terminal-race-consumer"
	notifications, err := f.store.ClaimUnreadNotifications(ctx, operation.FrameID, operation.RootFrameID, operation.OwnerUserID, claimToken, 1)
	if err != nil || len(notifications) != 1 || notifications[0].Payload["exec_id"] != operation.ExecutionID {
		t.Fatalf("claim exact result notification: notifications=%#v err=%v", notifications, err)
	}
	if count, err := f.store.AckClaimedNotifications(ctx, operation.FrameID, operation.RootFrameID, operation.OwnerUserID, claimToken, 1); err != nil || count != 1 {
		t.Fatalf("ack exact result notification: count=%d err=%v", count, err)
	}
}

func startRecoveryParkKernelOperation(t *testing.T, f *agentSaveArtifactsFixture) workspace.KernelLocalOperation {
	t.Helper()
	ctx := context.Background()
	run := &sessionRunnerChatRun{
		SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim},
	}
	call := agentruntime.ToolCall{ID: "terminal-race-background-call", Name: "python",
		Arguments: json.RawMessage(`{"code":"print('result')","environment":"python","background":true}`)}
	if err := f.server.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	operation, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, call.ID)
	if err != nil || !found {
		t.Fatalf("admit background operation: found=%t err=%v", found, err)
	}
	operation, err = f.store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID,
		ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID,
		Approved: true, DecisionID: "terminal-race-approval", Scope: "once",
		Source: "policy", ActorID: "system", CurrentClaim: f.claim,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err = f.store.PrepareKernelLocalOperation(ctx, workspace.PrepareKernelLocalOperationInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: f.claim, BootID: "terminal-race-boot", KernelID: "terminal-race-kernel",
		KernelGeneration: 1, ConfinementSHA256: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err = f.store.StartKernelLocalOperation(ctx, workspace.StartKernelLocalOperationInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: f.claim, BootID: operation.BootID, ExecutionID: "terminal-race-execution",
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func finishRecoveryParkKernelOperation(t *testing.T, f *agentSaveArtifactsFixture, operation workspace.KernelLocalOperation) workspace.FinishKernelLocalOperationResult {
	t.Helper()
	result := json.RawMessage(`{"ok":true,"executed":true,"exec_id":"terminal-race-execution","stdout":"result\n","exit_status":"ok"}`)
	notification := &workspace.CreateNotificationInput{
		ID: "terminal-race-result", SenderFrameID: operation.FrameID, RecipientFrameID: operation.FrameID,
		RootFrameID: operation.RootFrameID, OwnerUserID: operation.OwnerUserID, NotificationType: "cell_result",
		Payload: map[string]any{"exec_id": operation.ExecutionID, "tool_id": operation.ToolCallID, "status": "completed", "output": string(result)},
	}
	finished, err := f.store.FinishKernelLocalOperation(context.Background(), workspace.FinishKernelLocalOperationInput{
		OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: f.claim, BootID: operation.BootID, ExecutionID: operation.ExecutionID,
		TerminalState: workspace.KernelLocalOperationStateCompleted, ReasonCode: "execution_completed",
		TerminalResultJSON: result, Notification: notification,
		ExecutionLog: workspace.SaveExecutionLogInput{
			Record: workspace.ExecutionLogRecord{
				ID: operation.ExecutionID, FrameID: operation.FrameID, KernelID: operation.KernelID,
				CondaEnv: operation.Environment, Language: "python", Source: "print('result')",
				Stdout: "result\n", ExitStatus: "ok", Origin: "agent", ExecutedAt: time.Now().UTC(),
			},
			ExpectedOwnerID: operation.OwnerUserID, ExpectedProjectID: operation.ProjectID,
			ExpectedFrameIncarnationID: operation.FrameIncarnationID, ExpectedRootFrameIncarnationID: operation.RootFrameIncarnationID,
		},
	})
	if err != nil || !finished.Created {
		t.Fatalf("finish background operation: created=%t err=%v", finished.Created, err)
	}
	return finished
}
