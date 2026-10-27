package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	settingsstore "x-science/internal/persistence/settings"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestMemoryExtractionCompletedSourceRejectsUnconsumedAdmittedInput(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		before     bool
	}{
		{name: "compact queued before finish", mode: "compact", before: true},
		{name: "forked queued before finish", mode: "forked", before: true},
		{name: "compact queued after finish remains valid", mode: "compact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			f.server.memoryConfig = memoryconfig.Default()
			f.server.memoryConfig.Enabled = true
			f.server.memoryConfig.ExtractEnabled = true
			f.server.memoryConfig.ExtractMode = test.mode
			f.server.memoryConfig.ExtractEveryN = 1
			f.server.memoryConfig.PIClassifierEnabled = false
			f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
			if err := f.store.SetMemoryEnabled(ctx, f.stream.OwnerID, true); err != nil {
				t.Fatal(err)
			}
			claimedA := f.claim.ClaimedInputRevision
			appendB := func() {
				appendSkillControlInput(t, f, "memory-unconsumed-b", "B_UNCONSUMED_INPUT preserve this convention", false)
				if f.stream.InputRevision <= claimedA {
					t.Fatal("fixture did not admit B after A's claimed input")
				}
			}
			finish := func(id, text string) transcriptstore.Event {
				if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
					Claim: f.claim, ClientMessageID: id + "-answer", Type: "assistant_message",
					Source: transcriptstore.EventSourcePayload, PayloadJSON: []byte(`{"role":"assistant","text":"` + text + `"}`),
				}); err != nil {
					t.Fatal(err)
				}
				event, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
					Claim: f.claim, ClientMessageID: id + "-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
				})
				if err != nil {
					t.Fatal(err)
				}
				return event
			}
			if test.before {
				appendB()
			}
			finishA := finish("memory-unconsumed-a", "A_COMPLETED_EVIDENCE")
			if !test.before {
				appendB()
			}
			var calls atomic.Int64
			observed := make(chan string, 2)
			runtime := newMemoryExtractionRuntimeWithFactory(f.server, func(_ memorytools.Scope, _ string, _ *agentruntime.ModelRequest) memoryextract.Extractor {
				return memoryExtractorFunc(func(_ context.Context, input memoryextract.ExtractRequest) (map[string]any, error) {
					calls.Add(1)
					observed <- input.Transcript
					return map[string]any{}, nil
				})
			})
			runtime.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "A_REQUEST_VIEW"}}})
			if err := runCapturedMemorySourceFixture(ctx, runtime, f.stream.SessionID, finishA.EventID); err != nil {
				t.Fatal(err)
			}
			cursor, err := f.store.LastExtractMessageIndex(ctx, f.stream.FrameID)
			if err != nil {
				t.Fatal(err)
			}
			if !test.before {
				if calls.Load() != 1 || cursor <= 0 {
					t.Fatal("input admitted after the exact finish incorrectly invalidated A")
				}
				if strings.Contains(completedMemorySourceObservation(t, observed), "B_UNCONSUMED_INPUT") {
					t.Fatal("A included an input published after its exact finish")
				}
				return
			}
			if calls.Load() != 0 || cursor != 0 {
				t.Fatalf("A consumed unclaimed admitted B: calls=%d cursor=%d", calls.Load(), cursor)
			}
			claimSkillControlInput(t, f)
			if f.claim.ClaimedInputRevision <= claimedA {
				t.Fatal("B continuation did not claim its admitted input")
			}
			finishB := finish("memory-unconsumed-b", "B_COMPLETED_EVIDENCE")
			runtime.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "B_REQUEST_VIEW"}}})
			if err := runCapturedMemorySourceFixture(ctx, runtime, f.stream.SessionID, finishB.EventID); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("claimed B continuation did not reach extraction")
			}
			text := completedMemorySourceObservation(t, observed)
			if !strings.Contains(text, "A_COMPLETED_EVIDENCE") || !strings.Contains(text, "B_UNCONSUMED_INPUT") || !strings.Contains(text, "B_COMPLETED_EVIDENCE") {
				t.Fatal("B continuation lost the unextracted A/B history")
			}
			if cursor, err = f.store.LastExtractMessageIndex(ctx, f.stream.FrameID); err != nil || cursor <= 0 {
				t.Fatalf("B continuation did not advance its cursor: %d %v", cursor, err)
			}
		})
	}
}

func TestMemoryExtractionLegacyNumericAttemptUsesCommittedPayload(t *testing.T) {
	for _, test := range []struct {
		name    string
		runID   string
		attempt any
		wantRun bool
	}{
		{name: "empty run ID", attempt: 7, wantRun: true},
		{name: "unrelated numeric run ID", runID: "99", attempt: 7, wantRun: true},
		{name: "fractional payload is not an attempt", runID: "7", attempt: 7.5},
		{name: "missing payload cannot borrow run ID", runID: "7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := openWorkspaceMemoryToolServer(t)
			server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
			server.memoryConfig.ExtractEnabled = true
			server.memoryConfig.ExtractMode = "forked"
			server.memoryConfig.ExtractEveryN = 1
			server.memoryConfig.PIClassifierEnabled = false
			finish := eventjournal.Message{"type": "runner_finished", "status": "completed"}
			if test.attempt != nil {
				finish["runnerAttempt"] = test.attempt
			}
			var finishID int64
			for _, message := range []eventjournal.Message{
				{"type": "message", "role": "user", "text": "Remember this durable reporting convention."},
				{"type": "message", "role": "assistant", "text": "LEGACY_COMPLETED_EVIDENCE"},
				finish,
				{"type": "message", "role": "user", "text": "LATER_INPUT must remain excluded."},
			} {
				entry, err := server.eventJournal.Append("frame-root", message, eventjournal.Metadata{RunID: test.runID})
				if err != nil {
					t.Fatal(err)
				}
				if message["type"] == "runner_finished" {
					finishID = entry.EventID
				}
			}
			var calls atomic.Int64
			observed := make(chan string, 1)
			runtime := newMemoryExtractionRuntimeWithFactory(server, func(_ memorytools.Scope, _ string, request *agentruntime.ModelRequest) memoryextract.Extractor {
				return memoryExtractorFunc(func(_ context.Context, input memoryextract.ExtractRequest) (map[string]any, error) {
					calls.Add(1)
					if request == nil || len(request.Messages) != 1 {
						t.Error("legacy extraction lost its matching model request")
						return nil, nil
					}
					observed <- request.Messages[0].Content + "\n" + input.Transcript
					return map[string]any{}, nil
				})
			})
			runtime.rememberLastRequest("frame-root", 7, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "LEGACY_REQUEST_VIEW"}}})
			if err := <-runtime.ScheduleCompletedRootEvent("frame-root", finishID); err != nil {
				t.Fatal(err)
			}
			if !test.wantRun {
				if calls.Load() != 0 {
					t.Fatal("legacy extraction borrowed an unproven attempt")
				}
				return
			}
			if calls.Load() != 1 {
				t.Fatalf("committed numeric attempt did not reach extraction: calls=%d", calls.Load())
			}
			text := completedMemorySourceObservation(t, observed)
			if !strings.HasPrefix(text, "LEGACY_REQUEST_VIEW\n") || !strings.Contains(text, "LEGACY_COMPLETED_EVIDENCE") || strings.Contains(text, "LATER_INPUT") {
				t.Fatal("legacy extraction did not retain its exact completed source")
			}
		})
	}
}

func TestMemoryExtractionQueuedCompletionDoesNotBorrowLaterTurn(t *testing.T) {
	for _, fork := range []bool{false, true} {
		name := "later input and model request"
		if fork {
			name = "later completed replacement branch"
		}
		t.Run(name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			f.server.memoryConfig = memoryconfig.Default()
			f.server.memoryConfig.Enabled = true
			f.server.memoryConfig.ExtractEnabled = true
			f.server.memoryConfig.ExtractMode = "forked"
			f.server.memoryConfig.ExtractEveryN = 1
			f.server.memoryConfig.PIClassifierEnabled = false
			f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
			if err := f.store.SetMemoryEnabled(ctx, f.stream.OwnerID, true); err != nil {
				t.Fatal(err)
			}
			// Event IDs may have gaps while publication remains consecutive.
			if _, err := f.db.Exec(`UPDATE transcript_streams SET next_event_id=next_event_id+9 WHERE stream_uid=?`, f.stream.UID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
				Claim: f.claim, ClientMessageID: "memory-source-a-answer", Type: "assistant_message",
				Source: transcriptstore.EventSourcePayload, PayloadJSON: []byte(`{"role":"assistant","text":"A_FINISHED_EVIDENCE"}`),
			}); err != nil {
				t.Fatal(err)
			}
			finish, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
				Claim: f.claim, ClientMessageID: "memory-source-a-finish", Status: "completed",
				PayloadJSON: []byte(`{"status":"completed"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if finish.EventID == finish.PublicationSeq {
				t.Fatal("fixture must distinguish event ID from publication cutoff")
			}
			proof, err := f.repo.ReadCompletedMemorySource(ctx, f.stream.UID, f.stream.OwnerID, finish.EventID)
			if err != nil || proof.PublicationSeq != finish.PublicationSeq || proof.Attempt != f.claim.Attempt {
				t.Fatalf("exact completed receipt proof unavailable: %v", err)
			}
			if _, err := f.repo.ReadCompletedMemorySource(ctx, f.stream.UID, f.stream.OwnerID, finish.EventID-1); err == nil {
				t.Fatal("an assistant event was accepted as a completed receipt")
			}
			if _, err := f.repo.ReadCompletedMemorySource(ctx, f.stream.UID, "other-owner", finish.EventID); err == nil {
				t.Fatal("a foreign owner read completed source provenance")
			}
			var calls atomic.Int64
			observed := make(chan string, 1)
			runtime := newMemoryExtractionRuntimeWithFactory(f.server, func(_ memorytools.Scope, _ string, request *agentruntime.ModelRequest) memoryextract.Extractor {
				calls.Add(1)
				requestText := ""
				if request != nil && len(request.Messages) > 0 {
					requestText = request.Messages[0].Content
				}
				return memoryExtractorFunc(func(_ context.Context, input memoryextract.ExtractRequest) (map[string]any, error) {
					observed <- requestText + "\n" + input.Transcript
					return map[string]any{}, nil
				})
			})
			runtime.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "A_REQUEST_VIEW"}}})
			if scope, err := runtime.resolveCompletedRootMemoryScope(ctx, f.stream.SessionID); err != nil || !scope.Enabled {
				t.Fatalf("fixture memory policy must be enabled: %#v %v", scope, err)
			}
			// Source binding is tested directly, not through the retired canonical
			// RAM scheduler. Durable producer/delivery has its own restart tests.
			source, err := runtime.captureCompletedMemorySource(ctx, f.stream.SessionID, finish.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if fork {
				branch, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.repo.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: branch.ActiveBranchID,
					ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
					ClientMutationID: "memory-source-b-fork", SourceClientMessageID: "save-user", SourceMessageIndex: 0,
					ReplacementText: "B_LATER_INPUT corrected task", Destinations: []string{"ws"},
				}); err != nil {
					t.Fatal(err)
				}
				claimSkillControlInput(t, f)
				finishSkillControlInput(t, f)
			} else {
				appendSkillControlInput(t, f, "memory-source-b-user", "B_LATER_INPUT corrected task", false)
				claimSkillControlInput(t, f)
			}
			runtime.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "B_REQUEST_VIEW"}}})
			_, runErr := runtime.runCompletedRoot(ctx, f.stream.SessionID, source)
			runtime.discardAttemptRequest(f.stream.SessionID, source.attempt)
			if runErr != nil {
				t.Fatal(runErr)
			}
			if fork {
				if calls.Load() != 0 {
					t.Fatalf("old completion borrowed replacement branch: extractor calls=%d", calls.Load())
				}
				return
			}
			if calls.Load() != 1 {
				t.Fatalf("matching completed source was lost: calls=%d", calls.Load())
			}
			text := completedMemorySourceObservation(t, observed)
			if !strings.Contains(text, "A_FINISHED_EVIDENCE") || !strings.Contains(text, "A_REQUEST_VIEW") || strings.Contains(text, "B_LATER_INPUT") || strings.Contains(text, "B_REQUEST_VIEW") {
				t.Fatalf("completion extraction source drifted: %q", text)
			}
			if state := runtime.sessions[f.stream.SessionID]; state == nil || state.request == nil || state.requestAttempt != f.claim.Attempt {
				t.Fatal("A release discarded B's still-running request")
			}
			if _, _, err := f.repo.AppendRunnerEvent(ctx, transcriptstore.AppendEventInput{
				Claim: f.claim, ClientMessageID: "memory-source-b-answer", Type: "assistant_message",
				Source: transcriptstore.EventSourcePayload, PayloadJSON: []byte(`{"role":"assistant","text":"B_FINISHED_EVIDENCE"}`),
			}); err != nil {
				t.Fatal(err)
			}
			finishB, _, _, err := f.repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
				Claim: f.claim, ClientMessageID: "memory-source-b-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := runCapturedMemorySourceFixture(ctx, runtime, f.stream.SessionID, finishB.EventID); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 || !strings.HasPrefix(completedMemorySourceObservation(t, observed), "B_REQUEST_VIEW\n") {
				t.Fatal("B completion did not retain its own model request")
			}
			if len(runtime.sessions) != 0 {
				t.Fatal("settled extraction retained model request registry state")
			}
		})
	}
}

func completedMemorySourceObservation(t *testing.T, observed <-chan string) string {
	t.Helper()
	select {
	case value := <-observed:
		return value
	default:
		t.Fatal("completed extraction did not invoke its extractor")
		return ""
	}
}

// Exercises the internal source reader/write authority only. Canonical
// automatic scheduling is exclusively covered through the durable producer.
func runCapturedMemorySourceFixture(ctx context.Context, runtime *memoryExtractionRuntime, sessionID string, eventID int64) error {
	source, err := runtime.captureCompletedMemorySource(ctx, sessionID, eventID)
	if errors.Is(err, errMemoryExtractionSource) {
		return nil
	}
	if err != nil {
		return err
	}
	defer runtime.discardAttemptRequest(sessionID, source.attempt)
	_, err = runtime.runCompletedRoot(ctx, sessionID, source)
	return err
}

func TestMemoryExtractionRequestSnapshotIsDeepAndAttemptScoped(t *testing.T) {
	runtime := newMemoryExtractionRuntimeWithFactory(nil, nil)
	temperature := 0.2
	request := agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "assistant", ReasoningContent: "host reasoning", ContextUsageSource: agentruntime.ContextUsageSkills,
			Parts: []agentruntime.ContentPart{{Type: agentruntime.ContentPartText, Text: "A_PART"}, {Type: agentruntime.ContentPartImage,
				Media: &agentruntime.MediaContent{Source: agentruntime.MediaSource{Data: []byte("A_MEDIA")}}}},
			ToolCalls: []agentruntime.ToolCall{{Arguments: []byte(`{"a":1}`), PreExecutionRejection: []byte(`{"original":true}`), Resumed: true}},
		}},
		Tools: []agentruntime.ToolSchema{{Parameters: map[string]any{"nested": []any{map[string]any{"type": "A_SCHEMA"}}},
			OutputSchema: map[string]any{"type": "A_OUTPUT"}, Capabilities: []string{"A_CAPABILITY"}}},
		Metadata: map[string]any{"nested": []any{map[string]any{"value": "A_METADATA"}}},
		Headers:  map[string]string{"synthetic": "A_HEADER"}, ToolChoice: map[string]any{"function": map[string]any{"name": "A_TOOL"}},
		MediaPolicy: agentruntime.MediaPolicy{AllowedFileRoots: []string{"A_ROOT"}}, Temperature: &temperature,
	}
	runtime.rememberLastRequest("session", 1, request)
	request.Messages[0].Parts[0].Text = "B_PART"
	request.Messages[0].Parts[1].Media.Source.Data[0] = 'B'
	request.Messages[0].ToolCalls[0].Arguments[2] = 'b'
	request.Messages[0].ToolCalls[0].PreExecutionRejection[2] = 'b'
	request.Tools[0].Parameters["nested"].([]any)[0].(map[string]any)["type"] = "B_SCHEMA"
	request.Tools[0].OutputSchema["type"] = "B_OUTPUT"
	request.Tools[0].Capabilities[0] = "B_CAPABILITY"
	request.Metadata["nested"].([]any)[0].(map[string]any)["value"] = "B_METADATA"
	request.Headers["synthetic"] = "B_HEADER"
	request.ToolChoice.(map[string]any)["function"].(map[string]any)["name"] = "B_TOOL"
	request.MediaPolicy.AllowedFileRoots[0] = "B_ROOT"
	temperature = 0.9
	state := runtime.sessions["session"]
	frozen := state.request
	if state.requestAttempt != 1 || frozen.Messages[0].Parts[0].Text != "A_PART" || string(frozen.Messages[0].Parts[1].Media.Source.Data) != "A_MEDIA" ||
		string(frozen.Messages[0].ToolCalls[0].Arguments) != `{"a":1}` || string(frozen.Messages[0].ToolCalls[0].PreExecutionRejection) != `{"original":true}` ||
		!frozen.Messages[0].ToolCalls[0].Resumed || frozen.Messages[0].ReasoningContent != "host reasoning" || frozen.Messages[0].ContextUsageSource != agentruntime.ContextUsageSkills ||
		frozen.Tools[0].Parameters["nested"].([]any)[0].(map[string]any)["type"] != "A_SCHEMA" || frozen.Tools[0].OutputSchema["type"] != "A_OUTPUT" ||
		frozen.Tools[0].Capabilities[0] != "A_CAPABILITY" || frozen.Metadata["nested"].([]any)[0].(map[string]any)["value"] != "A_METADATA" ||
		frozen.Headers["synthetic"] != "A_HEADER" || frozen.ToolChoice.(map[string]any)["function"].(map[string]any)["name"] != "A_TOOL" ||
		frozen.MediaPolicy.AllowedFileRoots[0] != "A_ROOT" || *frozen.Temperature != 0.2 {
		t.Fatal("recorded typed request shares mutable containers or lost host-only fields")
	}
}

func TestMemoryExtractionOlderReleaseRetainsNextAttemptRequest(t *testing.T) {
	runtime := newMemoryExtractionRuntimeWithFactory(nil, nil)
	runtime.rememberLastRequest("session", 1, agentruntime.ModelRequest{})
	state := runtime.sessions["session"]
	state.queued, state.request = 1, nil
	runtime.rememberLastRequest("session", 2, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Content: "B"}}})
	runtime.releaseSessionState("session", state)
	runtime.discardAttemptRequest("session", 1)
	if runtime.sessions["session"] != state || state.request == nil || state.requestAttempt != 2 {
		t.Fatal("an old extraction/settlement released a later attempt's request")
	}
	runtime.discardAttemptRequest("session", 2)
	if len(runtime.sessions) != 0 {
		t.Fatal("non-extracting terminal attempt retained a complete request")
	}
}

func TestMemoryExtractionAtomicFinalizationReleasesOnlyCommittedRequest(t *testing.T) {
	for _, nextAttempt := range []bool{false, true} {
		name := "release settled request"
		if nextAttempt {
			name = "retain later request"
		}
		t.Run(name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			ctx := context.Background()
			if _, err := f.db.Exec(`UPDATE frames SET name='Aside cleanup fixture' WHERE id=?`, f.stream.FrameID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`CREATE TRIGGER memory_test_aside_output_failure BEFORE UPDATE OF output_data ON frame_runtime_metadata
			 BEGIN SELECT RAISE(ABORT,'synthetic aside output failure'); END`); err != nil {
				t.Fatal(err)
			}
			runtime := newMemoryExtractionRuntimeWithFactory(f.server, nil)
			f.server.memoryExtraction = runtime
			attempt := f.claim.Attempt
			if nextAttempt {
				attempt++
			}
			runtime.rememberLastRequest(f.stream.SessionID, attempt, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Content: "owned request"}}})
			authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
			run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority}
			if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
				t.Fatal(err)
			}
			result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
			err := f.server.settleSessionRunnerChatOutcome(ctx, SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result,
				&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil,
				"Completed response before the failing Aside projection.", nil, "")
			if err == nil || !strings.Contains(err.Error(), "synthetic aside output failure") {
				t.Fatalf("fixture did not reach the atomic Aside failure: %v", err)
			}
			if result.FinishEventID != 0 {
				t.Fatal("rolled-back finalization exposed an accepted terminal")
			}
			var finalEvents int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type IN ('assistant_message','runner_finished')`, f.stream.UID).Scan(&finalEvents); err != nil || finalEvents != 0 {
				t.Fatalf("Aside failure left partial final events: count=%d err=%v", finalEvents, err)
			}
			state := runtime.sessions[f.stream.SessionID]
			if state == nil || state.request == nil || state.requestAttempt != attempt {
				t.Fatal("uncommitted finalization discarded an owned request")
			}
			// The known rollback preserves the original claim and deterministic
			// identities. Retry only after removing the explicit fixture fault.
			if _, err := f.db.Exec(`DROP TRIGGER memory_test_aside_output_failure`); err != nil {
				t.Fatal(err)
			}
			err = f.server.settleSessionRunnerChatOutcome(ctx, SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result,
				&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil,
				"Completed response before the failing Aside projection.", nil, "")
			if err != nil || result.Status != "completed" || result.FinishEventID <= 0 {
				t.Fatalf("original finalization did not commit after known rollback: result=%#v err=%v", result, err)
			}
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type IN ('assistant_message','runner_finished')`, f.stream.UID).Scan(&finalEvents); err != nil || finalEvents != 2 {
				t.Fatalf("finalization was missing or duplicated: count=%d err=%v", finalEvents, err)
			}
			terminal, err := f.repo.GetTerminalProjection(ctx, f.stream.OwnerID, f.stream.UID, result.FinishEventID)
			if err != nil || terminal.TerminalStatus != "completed" {
				t.Fatalf("fixture did not commit the real terminal receipt: %#v %v", terminal, err)
			}
			state = runtime.sessions[f.stream.SessionID]
			if nextAttempt {
				if state == nil || state.request == nil || state.requestAttempt != attempt {
					t.Fatal("settled A discarded the next attempt's owned request")
				}
			} else if state != nil {
				t.Fatal("committed finish early return retained its completed request")
			}
		})
	}
	t.Run("uncommitted finish retains request", func(t *testing.T) {
		f := newAgentSaveArtifactsFixture(t)
		runtime := newMemoryExtractionRuntimeWithFactory(f.server, nil)
		f.server.memoryExtraction = runtime
		runtime.rememberLastRequest(f.stream.SessionID, f.claim.Attempt, agentruntime.ModelRequest{})
		stale := f.claim
		stale.ClaimToken = "not-the-owned-claim"
		authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: stale}
		run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority}
		if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
			t.Fatal(err)
		}
		result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
		err := f.server.settleSessionRunnerChatOutcome(context.Background(), SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result,
			&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, "", nil, "")
		if err == nil || result.FinishEventID != 0 || runtime.sessions[f.stream.SessionID] == nil {
			t.Fatal("unknown/uncommitted finish falsely released its request")
		}
	})
}
