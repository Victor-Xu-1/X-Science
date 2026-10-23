package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestConversationSkillSaveFeedbackUsesCanonicalLoadAndOriginalInput(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, f)
	const name = "feedback-original-contract"
	if _, err := f.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Inspect before<$ARGUMENTS>after.")); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, f, name, false)
	run, gateway := loadedClosureFixtureGateway(f)
	requested := json.RawMessage(`{"skill":"feedback-original-contract","args":"requested but not executed"}`)
	executed := json.RawMessage(`{"skill":"feedback-original-contract","args":" A "}`)
	loaded := appendConversationFeedbackLoad(t, f, run, gateway, "feedback-load", requested, executed)
	original := f.claim
	loadOutcome := requireConversationFeedbackCanonicalOutcome(t, f, "feedback-load")
	appendConversationFeedbackTerminal(t, f, run, "feedback-success", "completed", `{"ok":true,"value":"observed output"}`)
	appendConversationFeedbackTerminal(t, f, run, "feedback-failure", "failed", `{"ok":false,"error":"FAILED_STEP_REMAINS_UNRESOLVED"}`)
	successOutcome := requireConversationFeedbackCanonicalOutcome(t, f, "feedback-success")
	failureOutcome := requireConversationFeedbackCanonicalOutcome(t, f, "feedback-failure")
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	feedbackEvent := appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-correction", "Preserve the failed step; do not infer that its output exists.", false)
	claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-save", "INTERNAL_SAVE_IS_NOT_USER_FEEDBACK", true)
	saveRun := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-future", "FUTURE_INPUT_MUST_NOT_BECOME_FEEDBACK", false)
	source := readConversationFeedbackSource(t, f.agentSaveArtifactsFixture, saveRun)
	feedback := decodeConversationFeedbackSource(t, source.Transcript)
	if len(feedback.Loads) != 1 {
		t.Fatalf("canonical host load was not retained independently of prose: loads=%d", len(feedback.Loads))
	}
	load := feedback.Loads[0]
	if load.IdentityStatus != "proven" || load.Receipt == nil || !load.Receipt.BoundToCall("feedback-load") ||
		load.Receipt.InvocationKey != loaded.SkillLoadReceipt.InvocationKey ||
		load.Receipt.Binding.InputRevision != original.ClaimedInputRevision ||
		load.Receipt.Binding.RunnerAttempt != original.Attempt ||
		load.RequestMessageID != "save-user" || load.RunnerAttempt != original.Attempt ||
		load.SourceEventID != loadOutcome.SourceEventID || load.EventID != loadOutcome.TerminalEventID {
		t.Fatalf("load identity was inferred from the current Save input instead of original durable coordinates: %+v", load)
	}
	if load.ToolInput["args"] != "requested but not executed" || load.ExecutedToolInput["args"] != " A " {
		t.Fatalf("requested and actual argument bytes were conflated: requested=%#v executed=%#v", load.ToolInput, load.ExecutedToolInput)
	}
	for _, want := range []struct {
		call, phase string
		outcome     transcriptstore.RunnerReplayToolOutcome
	}{{"feedback-success", "completed", successOutcome}, {"feedback-failure", "failed", failureOutcome}} {
		terminal := findConversationFeedbackTerminal(t, feedback.ToolOutcomes, want.call)
		if terminal.ToolPhase != want.phase || terminal.CanonicalOutcome == nil || *terminal.CanonicalOutcome != want.outcome ||
			terminal.RequestMessageID != "save-user" || terminal.RunnerAttempt != original.Attempt ||
			!sameConversationFeedbackKeys(terminal.PriorLoadInvocationKeys, loaded.SkillLoadReceipt.InvocationKey) {
			t.Fatalf("terminal lost its canonical origin or prior-load set: %+v", terminal)
		}
		if want.phase == "failed" && !strings.Contains(string(terminal.ToolResult), "FAILED_STEP_REMAINS_UNRESOLVED") {
			t.Fatal("failed canonical terminal was silently promoted to successful evidence")
		}
	}
	if len(feedback.UserFeedback) != 1 || feedback.UserFeedback[0].EventID != feedbackEvent.EventID ||
		feedback.UserFeedback[0].Kind != "subsequent_user_feedback" ||
		!sameConversationFeedbackKeys(feedback.UserFeedback[0].PriorLoadInvocationKeys, loaded.SkillLoadReceipt.InvocationKey) {
		t.Fatalf("ordinary user input did not remain a subsequent observation: %+v", feedback.UserFeedback)
	}
	encoded, err := json.Marshal(feedback)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"INTERNAL_SAVE_IS_NOT_USER_FEEDBACK", "FUTURE_INPUT_MUST_NOT_BECOME_FEEDBACK", `"quality_score"`, `"auto_publish"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("feedback manufactured an instruction, score or later input: %q", forbidden)
		}
	}
}

func TestConversationSkillSaveFeedbackDoesNotInventAttribution(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	disableLoadedClosureFixtureMCP(t, f)
	run, gateway := loadedClosureFixtureGateway(f)
	var keys []string
	for index, name := range []string{"feedback-source-one", "feedback-source-two"} {
		if _, err := f.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Read the bounded synthetic procedure.")); err != nil {
			t.Fatal(err)
		}
		publishLoadedClosureFixture(t, f, name, false)
		args, _ := json.Marshal(map[string]any{"skill": name})
		callID := fmt.Sprintf("feedback-set-load-%d", index)
		loaded := appendConversationFeedbackLoad(t, f, run, gateway, callID, args, args)
		keys = append(keys, loaded.SkillLoadReceipt.InvocationKey)
		requireConversationFeedbackCanonicalOutcome(t, f, callID)
	}
	appendConversationFeedbackTerminal(t, f, run, "feedback-after-two-loads", "completed", `{"ok":true,"value":"observed, not attributed"}`)
	requireConversationFeedbackCanonicalOutcome(t, f, "feedback-after-two-loads")
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-set-user", "This is feedback after both loads, not a causal ranking.", false)
	next := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendConversationFeedbackTerminal(t, f, next, "feedback-new-input", "completed", `{"ok":true}`)
	requireConversationFeedbackCanonicalOutcome(t, f, "feedback-new-input")
	finishSkillControlInput(t, f.agentSaveArtifactsFixture)
	appendSkillControlInput(t, f.agentSaveArtifactsFixture, "feedback-set-save", "Do not classify this hidden Save as feedback.", true)
	save := claimSkillControlInput(t, f.agentSaveArtifactsFixture)
	feedback := decodeConversationFeedbackSource(t, readConversationFeedbackSource(t, f.agentSaveArtifactsFixture, save).Transcript)
	terminal := findConversationFeedbackTerminal(t, feedback.ToolOutcomes, "feedback-after-two-loads")
	if len(feedback.Loads) != 2 || !sameConversationFeedbackKeys(terminal.PriorLoadInvocationKeys, keys...) ||
		len(feedback.UserFeedback) != 1 || !sameConversationFeedbackKeys(feedback.UserFeedback[0].PriorLoadInvocationKeys, keys...) {
		t.Fatalf("multiple known loads were collapsed into one causal attribution: %+v", feedback)
	}
	for _, outcome := range feedback.ToolOutcomes {
		if outcome.ToolCallID == "feedback-new-input" && len(outcome.PriorLoadInvocationKeys) != 0 {
			t.Fatalf("a new admitted input silently inherited a prior active Skill version: %+v", outcome)
		}
	}
}

// These test-only views consume the existing source.Transcript. They do not
// depend on future production helpers, fields or a second feedback protocol.
type conversationFeedbackTestMetadata struct {
	Loads        []conversationFeedbackTestRecord `json:"loads"`
	ToolOutcomes []conversationFeedbackTestRecord `json:"tool_outcomes"`
	UserFeedback []conversationFeedbackTestRecord `json:"user_feedback"`
	Omitted      int                              `json:"omitted"`
}

type conversationFeedbackTestRecord struct {
	Kind                    string                                   `json:"kind"`
	IdentityStatus          string                                   `json:"identity_status"`
	EventID                 int64                                    `json:"event_id"`
	PublicationSequence     int64                                    `json:"publication_sequence"`
	RunnerAttempt           int64                                    `json:"runner_attempt"`
	RequestMessageID        string                                   `json:"request_message_id"`
	InputRevision           int64                                    `json:"input_revision"`
	SourceEventID           int64                                    `json:"source_event_id"`
	SourceRunnerAttempt     int64                                    `json:"source_runner_attempt"`
	SourceRequestMessageID  string                                   `json:"source_request_message_id"`
	SourceInputRevision     int64                                    `json:"source_input_revision"`
	ToolCallID              string                                   `json:"tool_call_id"`
	ToolPhase               string                                   `json:"tool_phase"`
	ToolInput               map[string]any                           `json:"tool_input"`
	ExecutedToolInput       map[string]any                           `json:"executed_tool_input"`
	ToolResult              json.RawMessage                          `json:"tool_result"`
	CanonicalOutcome        *transcriptstore.RunnerReplayToolOutcome `json:"canonical_outcome"`
	Receipt                 *agentruntime.SkillLoadReceipt           `json:"receipt"`
	PriorLoadInvocationKeys []string                                 `json:"prior_load_invocation_keys"`
}

func decodeConversationFeedbackSource(t *testing.T, text string) conversationFeedbackTestMetadata {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		var envelope struct {
			Feedback *conversationFeedbackTestMetadata `json:"skill_execution_feedback"`
		}
		if json.Unmarshal([]byte(line), &envelope) == nil && envelope.Feedback != nil {
			return *envelope.Feedback
		}
	}
	t.Fatal("canonical execution feedback metadata is missing from the ordinary Save source bundle")
	return conversationFeedbackTestMetadata{}
}

func appendConversationFeedbackLoad(t *testing.T, f *kernelSkillDraftFixture, run *sessionRunnerChatRun, gateway serverAgentRuntimeToolGateway, callID string, requested, executed json.RawMessage) agentruntime.ToolResult {
	t.Helper()
	options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID}
	call := agentruntime.ToolCall{ID: callID, Name: "skill", Arguments: requested}
	if err := f.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{
		Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(requested),
	}); err != nil {
		t.Fatal(err)
	}
	call.Arguments = executed
	result, err := gateway.Execute(ctx, call)
	if err != nil || !result.SkillLoadReceipt.BoundToCall(callID) {
		t.Fatalf("real host Skill load did not produce a bound receipt: receipt=%v err=%v", result.SkillLoadReceipt != nil, err)
	}
	if err := completeLoadedClosureFixture(ctx, f, options, run, callID, requested, result); err != nil {
		t.Fatal(err)
	}
	return result
}

func appendConversationFeedbackTerminal(t *testing.T, f *kernelSkillDraftFixture, run *sessionRunnerChatRun, callID, phase, result string) {
	t.Helper()
	options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID}
	call := agentruntime.ToolCall{ID: callID, Name: "read_file", Arguments: json.RawMessage(`{"file_path":"synthetic-notes.txt"}`)}
	if err := f.server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	terminalType := agentruntime.EventToolCompleted
	if phase == "failed" {
		terminalType = agentruntime.EventToolFailed
	}
	for _, event := range []agentruntime.Event{
		{Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments)},
		{Type: terminalType, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments), Result: result},
	} {
		if err := f.server.checkpointSessionRunnerToolEvent(context.Background(), options, run, event); err != nil {
			t.Fatal(err)
		}
	}
}

func requireConversationFeedbackCanonicalOutcome(t *testing.T, f *kernelSkillDraftFixture, callID string) transcriptstore.RunnerReplayToolOutcome {
	t.Helper()
	events, err := f.repo.ListRunnerReplay(context.Background(), transcriptstore.ListRunnerReplayInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, MessageLimit: 100, CheckpointLimit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ToolOutcome != nil && event.ToolOutcome.ToolCallID == callID {
			if event.ToolOutcome.SourceEventID <= 0 || event.ToolOutcome.TerminalEventID != event.Event.EventID {
				t.Fatalf("invalid real canonical terminal fixture: %+v", event.ToolOutcome)
			}
			return *event.ToolOutcome
		}
	}
	t.Fatalf("fixture has no canonical SQL batch/item relation for %s; absence is not a Save behavior RED", callID)
	return transcriptstore.RunnerReplayToolOutcome{}
}

func readConversationFeedbackSource(t *testing.T, f *agentSaveArtifactsFixture, run *sessionRunnerChatRun) conversationSkillControlSource {
	t.Helper()
	control, err := f.repo.ClaimedRunnerInputContext(context.Background(), run.Transcript.Claim)
	if err != nil || control == nil {
		t.Fatalf("control=%+v err=%v", control, err)
	}
	source, err := f.server.conversationSkillControlEvidence(context.Background(), run.Transcript, *control)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func findConversationFeedbackTerminal(t *testing.T, records []conversationFeedbackTestRecord, callID string) conversationFeedbackTestRecord {
	t.Helper()
	for _, record := range records {
		if record.ToolCallID == callID {
			return record
		}
	}
	t.Fatalf("canonical terminal %q is missing from bounded feedback", callID)
	return conversationFeedbackTestRecord{}
}

func sameConversationFeedbackKeys(actual []string, expected ...string) bool {
	if len(actual) != len(expected) {
		return false
	}
	set := make(map[string]bool, len(actual))
	for _, key := range actual {
		set[key] = true
	}
	for _, key := range expected {
		if !set[key] {
			return false
		}
	}
	return true
}
