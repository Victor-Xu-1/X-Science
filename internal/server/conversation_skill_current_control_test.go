package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestConversationSkillCurrentControlHTTPReplayAndRecovery(t *testing.T) {
	for _, mode := range []string{"new-save", "checkpoint-recovery", "failed-attempt-recovery", "following-ordinary"} {
		t.Run(mode, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			loadSkillControlCatalog(t, f)
			enabled := true
			if _, err := f.store.CreateAgent(workspace.CreateAgentInput{
				ID: "skill-control-agent", UserID: f.stream.OwnerID, Name: "OPERON", DisplayName: "Evidence assistant",
				Description:  "Synthetic assistant for current-control provider capture",
				SystemPrompt: "PROFILE_PROMPT_MUST_SURVIVE", Unrestricted: true, Enabled: &enabled,
				SkillNames: []string{"customize", "skill-creator"},
			}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.repo.AppendRunnerEvent(context.Background(), transcriptstore.AppendEventInput{
				Claim: f.claim, ClientMessageID: "old-summary", Type: "assistant_message", Source: transcriptstore.EventSourcePayload,
				PayloadJSON: []byte(`{"text":"Earlier work completed. This is the previous assistant summary."}`),
			}); err != nil {
				t.Fatal(err)
			}
			finishSkillControlInput(t, f)
			submitSkillControlHTTPInput(t, f, "old-control", "OLD_HIDDEN_CONTROL", true)
			claimSkillControlInput(t, f)
			finishSkillControlInput(t, f)
			wantControl := mode != "following-ordinary"
			text := "CURRENT_CONTROL_SENTINEL"
			if !wantControl {
				text = "Explain the recorded sequence."
			}
			inputID := submitSkillControlHTTPInput(t, f, "current-input", text, wantControl)
			run := claimSkillControlInput(t, f)
			input, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
			if err != nil || input == nil || input.ClientMessageID != inputID ||
				(input.InputData["turn_intent"] == conversationSkillControlIntent) != wantControl {
				t.Fatalf("HTTP intent was not the admitted input: input=%#v err=%v", input, err)
			}
			if mode == "checkpoint-recovery" || mode == "failed-attempt-recovery" {
				run = resumeSkillControlCheckpoint(t, f, mode == "failed-attempt-recovery")
			}
			entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), run.Transcript, 20, 100)
			if err != nil {
				t.Fatal(err)
			}
			session, err := f.server.loadTranscriptFrameSessionProjection(f.stream)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				calls.Add(1)
				messages, _ := request["messages"].([]any)
				assertCurrentSkillControlMessage(t, messages, wantControl)
				if !wantControl && promptContextFromMessages(decodeSkillControlProviderMessages(messages)) != text {
					t.Error("ordinary input was replaced by a historical Save action")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "No settled reusable procedure; no Skill was published."},
				}}})
			}))
			defer provider.Close()
			options := normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
				RunnerID: "skill-control-runner", Endpoint: provider.URL + "/v1/chat/completions", Model: "control-model",
				AllowedTools: []string{"skill"}, DisableMCPDiscovery: true, TranscriptResumeSource: f.claim.ResumeSource,
				RequestTimeout: 5 * time.Second, PreparationTimeout: 5 * time.Second, MaxToolRounds: 1, MaxAttempts: 1,
				ReplayLimit: 20, OutputLimitBytes: 64 * 1024,
			})
			result, err := f.server.runSessionRunnerChat(withTranscriptRunnerChatRun(context.Background(), run), options, session, entries, run)
			if err != nil || calls.Load() != 1 || !strings.Contains(result, "no Skill was published") {
				t.Fatalf("NoOp did not finish through the ordinary runner: calls=%d result=%q err=%v", calls.Load(), result, err)
			}
			if len(f.server.skillCatalog.Skills()) != 2 {
				t.Fatal("a NoOp model completion changed the published catalog")
			}
			if wantControl {
				assertSkillControlTaskScope(t, f, run)
			} else if run.currentControl != nil || run.TaskIntent != text {
				t.Fatalf("ordinary input inherited a control scope: control=%#v task=%q", run.currentControl, run.TaskIntent)
			}
		})
	}
}

func TestConversationSkillCurrentControlScopesCompactedReceipts(t *testing.T) {
	control := &sessionRunnerCurrentControl{intent: conversationSkillControlIntent, eventID: 10, publicationSeq: 10, clientMessageID: "control"}
	oldReceipt := sessionRunnerToolContinuityRecord{EventID: 2, ToolCallID: "earlier-science", ToolName: "skill", Phase: "completed",
		Successful: true, SkillName: "earlier-analysis", RequiredScientificCapabilities: []string{"earlier-capability"}}
	currentReceipt := sessionRunnerToolContinuityRecord{EventID: 11, ToolCallID: "current-customize", ToolName: "skill", Phase: "completed",
		Successful: true, SkillName: "customize"}
	old := eventjournal.Entry{EventID: 2, Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolResult": map[string]any{"artifacts": []any{map[string]any{
			"artifact_id": "earlier-structure", "version_id": "earlier-version",
		}}},
	}}
	compact := eventjournal.Entry{EventID: 12, Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolPhase": "auto_compact", "summary": "Earlier and current work retained.",
		sessionRunnerToolContinuityField: []sessionRunnerToolContinuityRecord{oldReceipt, currentReceipt},
	}}
	for _, entries := range [][]eventjournal.Entry{
		{old, {EventID: 10, ClientMessageID: "control", Message: eventjournal.Message{"type": "message", "role": "user", "text": "CURRENT_CONTROL_SENTINEL", "inputData": map[string]any{"turn_intent": conversationSkillControlIntent}}}, compact},
		{compact},      // Compaction has omitted the control bubble itself.
		{compact, old}, // An appended historical receipt keeps its original identity.
	} {
		scoped, err := control.scopeEntries(entries)
		if err != nil {
			t.Fatal(err)
		}
		if len(artifactReferencesFromRunnerEntries(scoped)) != 0 || len(requiredScientificCapabilitiesFromRunnerEntries(scoped)) != 0 {
			t.Fatal("earlier scientific artifacts or execution requirements entered current Save scope")
		}
		records, _, err := latestPersistedSessionRunnerToolContinuity(scoped)
		if err != nil || len(records) != 1 || records[0].ToolCallID != "current-customize" {
			t.Fatalf("current recovery receipt was lost: %#v %v", records, err)
		}
		original, _, err := latestPersistedSessionRunnerToolContinuity(entries)
		if err != nil || len(original) != 2 {
			t.Fatal("runtime projection changed persisted/recorded continuity evidence")
		}
	}
	if _, err := control.scopeEntries([]eventjournal.Entry{{EventID: 12, Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolPhase": "auto_compact", sessionRunnerToolContinuityField: "invalid ledger",
	}}}); err == nil {
		t.Fatal("control scoping hid a malformed continuity ledger")
	}
	scoped, err := control.scopeEntries([]eventjournal.Entry{old, {EventID: 11, Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolResult": map[string]any{"artifacts": []any{map[string]any{
			"artifact_id": "current-package", "version_id": "current-version",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if refs := artifactReferencesFromRunnerEntries(scoped); len(refs) != 1 || refs[0].ArtifactID != "current-package" {
		t.Fatalf("actual current artifact receipt was dropped: %#v", refs)
	}
	if missing := missingSessionRunnerRequiredDeliverables(conversationSkillContinuationInstruction, nil); len(missing) != 0 {
		t.Fatalf("conditional Save acquired a mandatory artifact/publication gate: %#v", missing)
	}
}

func decodeSkillControlProviderMessages(messages []any) []chatCompletionMessage {
	result := make([]chatCompletionMessage, 0, len(messages))
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		result = append(result, chatCompletionMessage{Role: role, Content: content})
	}
	return result
}
