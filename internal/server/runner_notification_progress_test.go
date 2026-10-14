package server

import (
	"context"
	"encoding/json"
	"testing"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNotificationObservationPreservesRecoveryUntilDeliveredWork(t *testing.T) {
	for _, test := range []struct {
		name     string
		result   map[string]any
		progress bool
	}{
		{"idle", map[string]any{"status": "idle", "num_notifications": 0, "notifications": []any{}}, false},
		{"legacy empty completed", map[string]any{"status": "completed", "num_notifications": 0, "notifications": []any{}}, false},
		{"timeout", map[string]any{"status": "timeout", "num_notifications": 0, "notifications": []any{}, "pending_work": map[string]any{"resource_waits": 1}}, false},
		{"uncollected", map[string]any{"status": "uncollected_results", "num_notifications": 0, "notifications": []any{}}, false},
		{"invalid received", map[string]any{"status": "received", "num_notifications": 1, "notifications": []any{}}, false},
		{"delivered capacity", map[string]any{"status": "received", "num_notifications": 1, "notifications": []any{map[string]any{"notification_type": "resource_capacity_admitted", "payload": map[string]any{"executed": false}}}}, true},
		{"delivered output", map[string]any{"status": "received", "num_notifications": 1, "notifications": []any{map[string]any{"notification_type": "cell_result"}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := agentruntime.ToolCall{Name: "python", Arguments: json.RawMessage(`{"code":"value=1","environment":"controlled"}`)}
			run := &sessionRunnerChatRun{}
			state := newSessionRunnerNoProgressRecovery(runnerRecoveryObligationFingerprint(nil, transcriptstore.RunnerInterruptionCause{}))
			state.recordNoProgress([]agentruntime.ToolCall{call})
			run.restoreNoProgressRecovery(state)
			run.generationRecovery = providerGenerationRecovery{Consecutive: 2}
			if got := run.completedSourceToolProgress("notification-call", "wait_for_notification", test.result); got != test.progress {
				t.Fatalf("live progress=%t want=%t", got, test.progress)
			} else if got {
				run.recordMaterialProgress()
				run.resetGenerationRecoveryProgress()
			}
			state.observeEntry(eventjournal.Entry{Message: eventjournal.Message{
				"type": "runner_checkpoint", "status": "failed", "toolPhase": prestartToolFailurePhase, "toolName": "python",
				"toolResult": map[string]any{"status": "resource_capacity_unavailable", "executed": false, "retryable": true},
			}}, nil)
			state.observeEntry(eventjournal.Entry{Message: eventjournal.Message{
				"type": "runner_checkpoint", "status": "completed", "toolPhase": "completed", "toolName": "wait_for_notification", "toolResult": test.result,
			}}, nil)
			wantNoProgress, wantGeneration := 1, 2
			if test.progress {
				wantNoProgress, wantGeneration = 0, 0
			}
			if run.NoProgressRecovery.Consecutive != wantNoProgress || state.Consecutive != wantNoProgress || run.generationRecoverySnapshot().Consecutive != wantGeneration {
				t.Fatalf("live/replay budgets changed inconsistently: live=%d replay=%d generation=%d", run.NoProgressRecovery.Consecutive, state.Consecutive, run.generationRecoverySnapshot().Consecutive)
			}
		})
	}
	if !runnerToolCompletionHasMaterialProgress("python", map[string]any{"ok": true, "effect": map[string]any{"state": "changed", "categories": []string{"workspace"}}}) {
		t.Fatal("fresh real computation stopped being progress")
	}
}

func TestNotificationIdleCheckpointDoesNotReopenDurableRecovery(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken, Transcript: authority}
	state := newSessionRunnerNoProgressRecovery(runnerRecoveryObligationFingerprint(authority, transcriptstore.RunnerInterruptionCause{}))
	call := agentruntime.ToolCall{Name: "python", Arguments: json.RawMessage(`{"code":"value=1","environment":"controlled"}`)}
	state.recordNoProgress([]agentruntime.ToolCall{call})
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "resource-budget-boundary", map[string]any{
		"status": "interrupted", "reason_code": sessionRunnerToolRoundNoProgressReasonCode, "resume_detail": state.resumeDetail("Capacity is unavailable."),
		"recovery_contract_revision": sessionRunnerRecoveryContractRevision,
	})
	run.restoreNoProgressRecovery(state)
	generation := providerGenerationRecovery{Version: 1, Scope: "provider-default", InputRevision: f.claim.ClaimedInputRevision, Consecutive: 2}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "resource-generation-budget", map[string]any{providerGenerationRecoveryField: generation})
	run.generationRecovery = generation
	wait := agentruntime.ToolCall{ID: "controlled-idle-wait", Name: "wait_for_notification", Arguments: json.RawMessage(`{"timeout_seconds":0,"human_description":"Checking pending work"}`)}
	options := SessionRunnerChatOptions{RunnerID: f.claim.RunnerID, SessionID: f.stream.SessionID}
	if err := f.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{wait}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []agentruntime.Event{
		{Type: agentruntime.EventToolStarted, ToolName: wait.Name, ToolCallID: wait.ID, Arguments: string(wait.Arguments)},
		{Type: agentruntime.EventToolCompleted, ToolName: wait.Name, ToolCallID: wait.ID, Arguments: string(wait.Arguments), Result: `{"status":"idle","num_notifications":0,"notifications":[]}`},
	} {
		if err := f.server.checkpointSessionRunnerToolEvent(context.Background(), options, run, event); err != nil {
			t.Fatal(err)
		}
	}
	if run.NoProgressRecovery.Consecutive != 1 || run.generationRecoverySnapshot().Consecutive != 2 {
		t.Fatal("actual live checkpoint reset capacity-failure budgets")
	}
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), authority, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	restored, found := sessionRunnerNoProgressRecoveryFromReplay(entries)
	if !found || restored.Consecutive != 1 || len(restored.ClosedActions) != 1 {
		t.Fatalf("durable replay lost budget/quarantine: %#v found=%t", restored, found)
	}
	replay := &sessionRunnerChatRun{SessionID: run.SessionID, Transcript: authority}
	for _, entry := range entries {
		if err := f.server.restoreRunnerProgressEntry(replay, entry); err != nil {
			t.Fatal(err)
		}
	}
	if replay.generationRecoverySnapshot().Consecutive != 2 {
		t.Fatal("generation replay treated empty idle as new work")
	}
}
