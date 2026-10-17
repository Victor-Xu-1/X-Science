package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

type compactionEpisodePersistenceFixture struct {
	server  *Server
	store   *workspace.Store
	db      *sql.DB
	run     *sessionRunnerChatRun
	options SessionRunnerChatOptions
	session sessionstore.Session
}

func newCompactionEpisodePersistenceFixture(t *testing.T) compactionEpisodePersistenceFixture {
	t.Helper()
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "episode-persistence-project", "episode-persistence-frame")
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { closeTestServer(t, server) })
	if _, err := server.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), float64(100)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "episode-persistence-frame", MessageUUID: "episode-persistence-input", ClientMessageID: "episode-persistence-input",
		Text: strings.Repeat("abcd", 2000),
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(t.Context(), "local", "episode-persistence-frame")
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "episode-persistence-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	session, err := server.loadTranscriptFrameSessionProjection(stream)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{
		SessionID: stream.SessionID, Attempt: int(claim.Claim.Attempt), ClaimToken: claim.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claim.Claim},
	}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	return compactionEpisodePersistenceFixture{
		server: server, store: store, db: db, run: run, session: session,
		options: SessionRunnerChatOptions{SessionID: stream.SessionID, RunnerID: claim.Claim.RunnerID, Model: "test-model", ReplayLimit: 100},
	}
}

func (fixture *compactionEpisodePersistenceFixture) resume(t *testing.T, reason string) {
	t.Helper()
	prior := fixture.run.Transcript.Claim
	interrupted, err := fixture.server.transcriptStore.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
		Claim: prior, ClientMessageID: transcriptRunnerClientMessageID(prior, "episode-resume-"+reason), ReasonCode: reason, AutoResume: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := fixture.server.transcriptStore.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: prior.StreamUID, OwnerID: prior.OwnerID, RunnerID: "episode-persistence-resumed",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !resumed.Claimed || resumed.Claim.ClaimToken == prior.ClaimToken ||
		resumed.Claim.Attempt != prior.Attempt || resumed.Claim.ClaimedInputRevision != prior.ClaimedInputRevision {
		t.Fatalf("resume compact episode claim=%#v err=%v", resumed, err)
	}
	fixture.options.RunnerID = resumed.Claim.RunnerID
	fixture.run = &sessionRunnerChatRun{
		SessionID: fixture.session.ID, Attempt: int(resumed.Claim.Attempt), ClaimToken: resumed.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: fixture.run.Transcript.Stream, Claim: resumed.Claim},
	}
	if err := ensureSessionRunnerExecutionPhaseMachine(fixture.run); err != nil {
		t.Fatal(err)
	}
}

func (fixture *compactionEpisodePersistenceFixture) completeTool(t *testing.T, call agentruntime.ToolCall, result string, observer ...func()) string {
	t.Helper()
	if err := fixture.server.checkpointChatModelToolCalls(fixture.options, fixture.run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments), Result: result,
	}, observer...); err != nil {
		t.Fatal(err)
	}
	batchID := fixture.run.ToolBatchIDs[call.ID]
	if batchID == "" {
		t.Fatal("canonical fixture did not return a batch identity")
	}
	return batchID
}

func requireCompactionEpisodeOutcome(t *testing.T, entries []eventjournal.Entry, batchID string) (eventjournal.Entry, transcriptstore.RunnerReplayToolOutcome) {
	t.Helper()
	for _, entry := range entries {
		if outcome, ok := entry.RuntimeProjection.(transcriptstore.RunnerReplayToolOutcome); ok && outcome.BatchID == batchID {
			return entry, outcome
		}
	}
	t.Fatalf("actual Transcript replay omitted canonical outcome for batch %q", batchID)
	return eventjournal.Entry{}, transcriptstore.RunnerReplayToolOutcome{}
}

func TestRunnerCompactionEpisodeLegacyPreparationBindsOnce(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	// This is the old production checkpoint shape. Do not invent the model
	// identity that its writer did not record or rewrite the original event.
	if err := fixture.server.checkpointChatTool(fixture.options, fixture.run, "completed", "legacy compact", "", "auto_compact", map[string]any{
		"summary": "legacy durable handoff", "trigger": "auto", "threshold": 100, "contextWindow": 0,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.resume(t, "runtime_draining")
	loadReplay := func() []eventjournal.Entry {
		entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	legacy := loadReplay()
	_, first, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, legacy, fixture.run)
	if err != nil || !first.Triggered {
		t.Fatalf("legacy transition=%+v err=%v", first, err)
	}
	archives, err := fixture.store.ListCompactionArchives(fixture.session.ID)
	if err != nil || len(archives) != 1 {
		t.Fatalf("normal transition archives=%d err=%v", len(archives), err)
	}
	if err := fixture.server.checkpointSessionRunnerPreparationStage(t.Context(), fixture.run, "context_replay"); err != nil {
		t.Fatal(err)
	}
	_, second, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, loadReplay(), fixture.run)
	if err != nil || second.Triggered {
		t.Fatalf("bound transition repeated=%+v err=%v", second, err)
	}
	after, err := fixture.store.ListCompactionArchives(fixture.session.ID)
	if err != nil || !reflect.DeepEqual(after, archives) {
		t.Fatalf("bound transition changed archives: %v", err)
	}
	var compacts, legacyUnbound int
	if err := fixture.db.QueryRow(`SELECT COUNT(*),SUM(CASE WHEN json_type(payload_json,'$.softCompactionEpisode') IS NULL THEN 1 ELSE 0 END)
		FROM transcript_events WHERE stream_uid=? AND event_type='runner_checkpoint' AND json_extract(payload_json,'$.toolPhase')='auto_compact'`,
		fixture.run.Transcript.Stream.UID).Scan(&compacts, &legacyUnbound); err != nil || compacts != 2 || legacyUnbound != 1 {
		t.Fatalf("compacts=%d legacy unbound=%d err=%v", compacts, legacyUnbound, err)
	}
}

func TestRunnerCompactionEpisodeLegacyWithoutSoftPressureDoesNotUpgrade(t *testing.T) {
	for _, threshold := range []int{100000, 0} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			fixture := newCompactionEpisodePersistenceFixture(t)
			if _, err := fixture.server.settingsStore.Set(configStoreKey("autoCompactTokenThreshold"), float64(threshold)); err != nil {
				t.Fatal(err)
			}
			if err := fixture.server.checkpointChatTool(fixture.options, fixture.run, "completed", "legacy compact", "", "auto_compact", map[string]any{
				"summary": "legacy durable handoff", "trigger": "auto",
			}); err != nil {
				t.Fatal(err)
			}
			entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
			if err != nil {
				t.Fatal(err)
			}
			_, result, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
			if err != nil || result.Triggered || result.Threshold != threshold {
				t.Fatalf("legacy format alone triggered compaction: result=%+v err=%v", result, err)
			}
			archives, err := fixture.store.ListCompactionArchives(fixture.session.ID)
			if err != nil || len(archives) != 0 {
				t.Fatalf("format-only compaction archives=%d err=%v", len(archives), err)
			}
		})
	}
}

func TestRunnerCompactionEpisodeFreshProviderPressureStillCompacts(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !first.Triggered {
		t.Fatalf("initial compact=%+v err=%v", first, err)
	}
	fixture.resume(t, sessionRunnerProviderContextPressureReasonCode)
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil || !runnerReplayFreshlyCompacted(entries) || runnerContextPressureRequiringCompaction(entries) != sessionRunnerProviderContextPressureReasonCode {
		t.Fatalf("fresh pressure fixture err=%v", err)
	}
	updated, second, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !second.Triggered || second.TriggerReason != sessionRunnerProviderContextPressureReasonCode ||
		runnerContextPressureRequiringCompaction(updated) != "" {
		t.Fatalf("fresh episode erased genuine provider pressure: result=%+v err=%v", second, err)
	}
}

func TestRunnerCompactionEpisodeUnknownLegacyOutcomePassesThroughAndKeepsPressureRecovery(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !first.Triggered {
		t.Fatalf("initial compact=%+v err=%v", first, err)
	}
	// Legacy completed Skill receipts may be pinned outside the provider's
	// call window. Their existing compatibility projection permits the request,
	// but without a canonical proposal they cannot prove new execution here.
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolName: "Skill", ToolCallID: "legacy-pinned-capability",
		Arguments: `{"skill":"legacy-capability"}`, Result: `{"ok":true,"contract":"existing legacy capability"}`,
	}); err != nil {
		t.Fatal(err)
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	budget := newSessionRunnerRequestContextBudget(fixture.server, fixture.options, entries, fixture.run, nil)
	if budget.episode.armed {
		t.Fatal("unknown legacy provenance was labeled new progress")
	}
	messages, err := sessionEntriesToProviderMessages(fixture.options.SystemPrompt, entries)
	if err != nil {
		t.Fatal(err)
	}
	delegate := &preResolvedDynamicModelClient{}
	client := &sessionRunnerDynamicModelClient{
		initialReady: true, initial: sessionRunnerResolvedModelClient{client: delegate, model: fixture.options.Model},
		contextOptions: fixture.options, contextBudget: budget,
	}
	if _, err := client.Complete(t.Context(), agentruntime.ModelRequest{Messages: agentRuntimeMessagesFromChat(messages)}); err != nil || delegate.calls.Load() != 1 {
		t.Fatalf("unknown legacy outcome blocked request: calls=%d err=%v", delegate.calls.Load(), err)
	}
	fixture.resume(t, sessionRunnerProviderContextPressureReasonCode)
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	updated, forced, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !forced.Triggered || forced.TriggerReason != sessionRunnerProviderContextPressureReasonCode ||
		runnerContextPressureRequiringCompaction(updated) != "" {
		t.Fatalf("unknown legacy outcome swallowed actual pressure: result=%+v err=%v", forced, err)
	}
}

func TestRunnerCompactionEpisodeTerminalObserverRequiresNewCommittedOutcome(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, compact, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !compact.Triggered {
		t.Fatalf("initial compact=%+v err=%v", compact, err)
	}
	budget := newSessionRunnerRequestContextBudget(fixture.server, fixture.options, entries, fixture.run, compact.episode)
	observed := 0
	observer := func() {
		observed++
		budget.noteDurableProgress(t.Context())
	}
	call := agentruntime.ToolCall{ID: "episode-terminal", Name: "web_fetch", Arguments: json.RawMessage(`{}`)}
	if err := fixture.server.checkpointChatModelToolCalls(fixture.options, fixture.run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: `{}`,
	}, observer); err != nil {
		t.Fatal(err)
	}
	if observed != 0 || budget.episode.armed {
		t.Fatal("proposal or start rearmed the compact episode")
	}
	event := agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID, Result: `{"ok":true,"observation":"first"}`}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event, observer); err != nil {
		t.Fatal(err)
	}
	if observed != 1 || !budget.episode.armed {
		t.Fatalf("new committed terminal observed=%d armed=%t", observed, budget.episode.armed)
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil || !restoreRunnerCompactionEpisode(compact.episode.binding, entries).armed {
		t.Fatalf("new canonical batch outcome did not rearm durable replay: %v", err)
	}
	_, large := requestBudgetFixture()
	var pressure *sessionRunnerRequestContextPressureError
	if err := budget.beforeCall(t.Context(), large); !errors.As(err, &pressure) {
		t.Fatalf("new terminal did not rearm request admission: %v", err)
	}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event, observer); err != nil {
		t.Fatal(err)
	}
	event.Result = `{"ok":true,"observation":"divergent"}`
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event, observer); err != nil {
		t.Fatal(err)
	}
	if observed != 1 || budget.episode.armed {
		t.Fatalf("replay/conflict rearmed consumed episode: observed=%d armed=%t", observed, budget.episode.armed)
	}
}

func TestRunnerCompactionEpisodeTerminalObserverRejectsUnexecutedAndRollback(t *testing.T) {
	for _, mode := range []string{"prestart", "rejected_completed", "executed_false", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCompactionEpisodePersistenceFixture(t)
			call := agentruntime.ToolCall{ID: "episode-" + mode, Name: "web_fetch", Arguments: json.RawMessage(`{}`)}
			if err := fixture.server.checkpointChatModelToolCalls(fixture.options, fixture.run, []agentruntime.ToolCall{call}); err != nil {
				t.Fatal(err)
			}
			event := agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID, Result: `{"ok":true}`}
			if mode == "prestart" {
				event.Type, event.RejectedBeforeExecution = agentruntime.EventToolFailed, true
			} else if mode == "rejected_completed" {
				event.RejectedBeforeExecution = true
				event.Result = `{"code":"agent_owned_decision","executed":false}`
			} else if mode == "executed_false" {
				event.Result = `{"ok":true,"executed":false}`
			}
			if mode == "rollback" || mode == "executed_false" {
				if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
					Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: `{}`,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "rollback" {
				if _, err := fixture.db.Exec(`CREATE TRIGGER reject_episode_terminal BEFORE UPDATE OF state ON transcript_tool_call_items
					WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'controlled terminal rollback'); END`); err != nil {
					t.Fatal(err)
				}
			}
			observed := 0
			err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event, func() { observed++ })
			if mode == "rollback" && (err == nil || !strings.Contains(err.Error(), "controlled terminal rollback")) || mode != "rollback" && err != nil {
				t.Fatalf("terminal mode=%s err=%v", mode, err)
			}
			if observed != 0 {
				t.Fatalf("%s terminal observer calls=%d", mode, observed)
			}
		})
	}
}

func TestRunnerCompactionEpisodeOldTerminalRepublishedAfterClaimDoesNotRearm(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	call := agentruntime.ToolCall{ID: "episode-old-terminal", Name: "web_fetch", Arguments: json.RawMessage(`{}`)}
	if err := fixture.server.checkpointChatModelToolCalls(fixture.options, fixture.run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	event := agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID, Result: `{"ok":true,"observation":"original"}`}
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event); err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, compact, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !compact.Triggered {
		t.Fatalf("compact after original terminal=%+v err=%v", compact, err)
	}
	priorBatches, priorOrdinals := fixture.run.ToolBatchIDs, fixture.run.ToolBatchOrdinals
	fixture.resume(t, "runtime_draining")
	// Reuse only the batch identity actually returned by the first canonical
	// proposal. This models a late old outcome delivered to the new claim.
	fixture.run.ToolBatchIDs, fixture.run.ToolBatchOrdinals = priorBatches, priorOrdinals
	budget := newSessionRunnerRequestContextBudget(fixture.server, fixture.options, entries, fixture.run, compact.episode)
	observed := 0
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, event, func() {
		observed++
		budget.noteDurableProgress(t.Context())
	}); err != nil {
		t.Fatal(err)
	}
	if observed != 0 || budget.episode.armed {
		t.Fatal("old batch outcome rearmed the live episode under its new claim")
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if restoreRunnerCompactionEpisode(compact.episode.binding, entries).armed {
		t.Fatal("restart mistook a republished old terminal for new progress")
	}
}

func TestRunnerCompactionEpisodeSameAttemptReusedCallIDKeepsCanonicalProgress(t *testing.T) {
	fixture := newCompactionEpisodePersistenceFixture(t)
	originalClaim := fixture.run.Transcript.Claim
	call := agentruntime.ToolCall{ID: "episode-reused-id", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.test/original"}`)}
	oldResult := `{"ok":true,"observation":"original"}`
	oldBatch := fixture.completeTool(t, call, oldResult)
	entries, err := fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, oldOutcome := requireCompactionEpisodeOutcome(t, entries, oldBatch)
	_, first, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !first.Triggered {
		t.Fatalf("initial compact=%+v err=%v", first, err)
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	continuity, firstBoundaryID, err := latestPersistedSessionRunnerToolContinuity(entries)
	if err != nil || firstBoundaryID <= oldOutcome.TerminalEventID ||
		len(continuity) != 1 || continuity[0].ToolCallID != call.ID || continuity[0].EventID != oldOutcome.TerminalEventID {
		t.Fatalf("fixture lost the actual old ID continuity: continuity=%+v err=%v", continuity, err)
	}

	fixture.resume(t, "runtime_draining")
	budget := newSessionRunnerRequestContextBudget(fixture.server, fixture.options, entries, fixture.run, first.episode)
	observed := 0
	call.Arguments = json.RawMessage(`{"url":"https://example.test/new-proposal"}`)
	newBatch := fixture.completeTool(t, call, `{"ok":true,"observation":"new"}`, func() {
		observed++
		budget.noteDurableProgress(t.Context())
	})
	if newBatch == oldBatch || observed != 1 || !budget.episode.armed {
		t.Fatalf("new batch reused the old origin or lost live progress: sameBatch=%t observed=%d armed=%t", newBatch == oldBatch, observed, budget.episode.armed)
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if reason := runnerContextPressureRequiringCompaction(entries); reason != "" {
		t.Fatalf("new outcome fixture has unrelated pressure %q", reason)
	}
	newEntry, newOutcome := requireCompactionEpisodeOutcome(t, entries, newBatch)
	if fixture.run.Transcript.Claim.Attempt != originalClaim.Attempt || newEntry.Message["runnerAttempt"] != originalClaim.Attempt ||
		newOutcome.ToolCallID != oldOutcome.ToolCallID || newOutcome.SourceEventID <= firstBoundaryID ||
		newOutcome.SourceEventID == oldOutcome.SourceEventID || newOutcome.TerminalEventID <= firstBoundaryID {
		t.Fatalf("same-attempt reused ID did not retain distinct canonical coordinates: old=%+v new=%+v", oldOutcome, newOutcome)
	}
	if !runnerCompactionNewToolOutcome(eventjournal.Entry{EventID: firstBoundaryID}, newEntry) ||
		!restoreRunnerCompactionEpisode(first.episode.binding, entries).armed {
		t.Fatal("old ID-only continuity suppressed a genuinely new canonical batch outcome")
	}

	// Consume that genuine progress through normal compaction, then deliver the
	// old batch result under another new claim of this same attempt.
	_, second, err := fixture.server.autoCompactSessionForRunner(t.Context(), fixture.options, fixture.session, entries, fixture.run)
	if err != nil || !second.Triggered {
		t.Fatalf("compact after new canonical outcome=%+v err=%v", second, err)
	}
	fixture.resume(t, "runtime_draining")
	fixture.run.ToolBatchIDs = map[string]string{call.ID: oldBatch}
	fixture.run.ToolBatchOrdinals = map[string]int64{call.ID: oldOutcome.Ordinal}
	observed = 0
	budget = newSessionRunnerRequestContextBudget(fixture.server, fixture.options, nil, fixture.run, second.episode)
	if err := fixture.server.checkpointSessionRunnerToolEvent(t.Context(), fixture.options, fixture.run, agentruntime.Event{
		Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID,
		Arguments: `{"url":"https://example.test/original"}`, Result: oldResult,
	}, func() {
		observed++
		budget.noteDurableProgress(t.Context())
	}); err != nil {
		t.Fatal(err)
	}
	entries, err = fixture.server.loadTranscriptRunnerReplay(t.Context(), fixture.run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, secondBoundaryID, err := latestPersistedSessionRunnerToolContinuity(entries)
	if err != nil {
		t.Fatal(err)
	}
	republished := 0
	for _, entry := range entries {
		if entry.EventID > secondBoundaryID && stringValue(entry.Message["toolCallId"]) == call.ID &&
			stringValue(entry.Message["toolPhase"]) == "completed" {
			republished++
			if entry.RuntimeProjection != nil || runnerCompactionNewToolOutcome(eventjournal.Entry{EventID: secondBoundaryID}, entry) {
				t.Fatal("old terminal republish acquired a new canonical origin")
			}
		}
	}
	if republished != 1 || observed != 0 || budget.episode.armed || restoreRunnerCompactionEpisode(second.episode.binding, entries).armed {
		t.Fatalf("republished old outcome rearmed after ID reuse: republished=%d observed=%d liveArmed=%t", republished, observed, budget.episode.armed)
	}
}
