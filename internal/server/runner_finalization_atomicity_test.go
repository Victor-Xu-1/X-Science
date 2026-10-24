package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sessionstore "x-science/internal/persistence/sessions"
)

func TestRunnerFinalizationRollsBackAssistantWhenTerminalPersistenceFails(t *testing.T) {
	caseStarted := time.Now()
	fixture := newAgentSaveArtifactsFixture(t)
	if fixture.server.kernelManager != nil || len(fixture.server.agentRuntimeHooksForEvent("Stop", "")) != 0 {
		t.Fatal("SETUP_FAILURE: atomicity fixture must have no KernelManager or Stop hooks")
	}
	// This single storage probe uses the original normal settlement path. Only
	// this test object disables optional Memory; no model or tool is invoked.
	fixture.server.memoryExtraction = nil
	ctx := context.Background()
	authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}
	run := &sessionRunnerChatRun{SessionID: fixture.stream.SessionID, Attempt: int(fixture.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatalf("SETUP_FAILURE: initialize execution phase: %v", err)
	}
	activeRun := &activeSessionRun{}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	const finalAnswer = "Complete synthetic final answer: preserve every word through terminal persistence rejection and exact-identity continuation."
	const triggerName = "reject_atomicity_probe_runner_terminal"
	const triggerSQL = `CREATE TRIGGER reject_atomicity_probe_runner_terminal
		BEFORE INSERT ON transcript_events
		WHEN NEW.event_type = 'runner_finished'
		BEGIN SELECT RAISE(ABORT, 'synthetic terminal rejection'); END`
	if _, err := fixture.db.ExecContext(ctx, triggerSQL); err != nil {
		t.Fatalf("SETUP_FAILURE: install persistent trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := fixture.db.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName); err != nil {
			t.Errorf("drop atomicity trigger during cleanup: %v", err)
		}
	})
	var triggers int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, triggerName).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("SETUP_FAILURE: trigger must be in the real SQLite schema, count=%d err=%v", triggers, err)
	}
	t.Logf("ATOMICITY_SETUP_READY: real persistent trigger installed; no kernels/hooks/Memory; elapsed_seconds=%.6f", time.Since(caseStarted).Seconds())
	settle := func() error {
		return fixture.server.settleSessionRunnerChatOutcome(ctx,
			SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}, result, activeRun,
			sessionstore.RunnerMutationClaim{}, authority, run,
			sessionstore.Session{ID: run.SessionID}, nil, finalAnswer, nil, "")
	}
	countRows := func(query string) int {
		t.Helper()
		var count int
		if err := fixture.db.QueryRowContext(ctx, query, fixture.stream.UID).Scan(&count); err != nil {
			t.Fatalf("read actual persisted row count: %v", err)
		}
		return count
	}
	if err := settle(); err == nil || !strings.Contains(err.Error(), "synthetic terminal rejection") {
		t.Fatalf("SETUP_FAILURE: normal settlement must return the injected real terminal SQL error, got %v", err)
	}
	answers := countRows(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='assistant_message'`)
	refs := countRows(`SELECT COUNT(*) FROM transcript_artifact_refs WHERE stream_uid=?`)
	terminals := countRows(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_finished'`)
	receipts := countRows(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`)
	t.Logf("ATOMICITY_REJECTED: assistant=%d refs=%d terminal=%d receipts=%d active_settled=%t finish_event_id=%d", answers, refs, terminals, receipts, activeRun.settled, result.FinishEventID)
	if terminals != 0 || receipts != 0 {
		t.Fatalf("SETUP_FAILURE: persistent trigger failed to reject the real terminal commit: terminal=%d receipts=%d", terminals, receipts)
	}
	if answers != 0 || refs != 0 {
		// Record the actual RED without aborting the original-identity recovery.
		t.Errorf("ATOMICITY_RED: terminal rejection left partial assistant/artifact publication: assistant=%d refs=%d; want 0/0", answers, refs)
	}
	if activeRun.settled || result.FinishEventID != 0 {
		t.Errorf("settlement claimed completion after terminal SQL rejection: settled=%t finish_event_id=%d", activeRun.settled, result.FinishEventID)
	}
	state, err := fixture.repo.GetRunnerRuntimeState(ctx, fixture.stream.UID, fixture.stream.OwnerID, fixture.claim.Attempt)
	if err != nil || state.Status != "running" || state.FinishedEventID != 0 {
		t.Fatalf("original runner claim changed after terminal rejection: state=%#v err=%v", state, err)
	}
	if _, err := fixture.db.ExecContext(ctx, "DROP TRIGGER "+triggerName); err != nil {
		t.Fatalf("explicitly drop persistent trigger before exact-identity continuation: %v", err)
	}
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, triggerName).Scan(&triggers); err != nil || triggers != 0 {
		t.Fatalf("trigger cleanup incomplete: count=%d err=%v", triggers, err)
	}
	if err := settle(); err != nil {
		t.Fatalf("same original claim and IDs did not settle after explicit trigger removal: %v", err)
	}
	answers = countRows(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='assistant_message'`)
	terminals = countRows(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='runner_finished'`)
	receipts = countRows(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`)
	if answers != 1 || terminals != 1 || receipts != 1 || !activeRun.settled || result.Status != "completed" || result.FinishEventID == 0 {
		t.Fatalf("exact-identity continuation duplicated or lost completion: assistant=%d terminal=%d receipts=%d settled=%t result=%#v", answers, terminals, receipts, activeRun.settled, result)
	}
	var rawAnswer []byte
	if err := fixture.db.QueryRowContext(ctx, `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND client_message_id=? AND event_type='assistant_message'`,
		fixture.stream.UID, transcriptRunnerClientMessageID(fixture.claim, "assistant-message")).Scan(&rawAnswer); err != nil {
		t.Fatalf("read the original assistant identity: %v", err)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rawAnswer, &payload); err != nil || payload.Text != finalAnswer {
		t.Fatalf("exact final answer was not retained: text=%q err=%v", payload.Text, err)
	}
	terminal, found, err := fixture.repo.GetEventByClientMessageID(ctx, fixture.stream.UID, fixture.stream.OwnerID, transcriptRunnerClientMessageID(fixture.claim, "runner-finished"))
	if err != nil || !found || terminal.EventID != result.FinishEventID {
		t.Fatalf("original terminal identity not retained: found=%t event=%#v err=%v", found, terminal, err)
	}
	projection, err := fixture.repo.GetTerminalProjection(ctx, fixture.stream.OwnerID, fixture.stream.UID, result.FinishEventID)
	if err != nil || projection.TerminalStatus != "completed" {
		t.Fatalf("canonical terminal projection not completed: projection=%#v err=%v", projection, err)
	}
	t.Log("ATOMICITY_RECOVERY: trigger dropped; same original claim and IDs retained the complete answer, one assistant and one terminal; cleanup complete")
}
