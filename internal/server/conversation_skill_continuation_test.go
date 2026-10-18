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

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestConversationSkillContinuationUsesNormalRunnerTools(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	loadSkillControlCatalog(t, f)
	enabled := true
	if _, err := f.store.CreateAgent(workspace.CreateAgentInput{
		ID: "skill-control-agent", UserID: f.stream.OwnerID, Name: "OPERON", DisplayName: "Evidence assistant",
		Description: "Preserves current assistant authority.", SystemPrompt: "PROFILE_PROMPT_MUST_SURVIVE",
		Unrestricted: true, Enabled: &enabled, SkillNames: []string{"customize", "skill-creator"},
	}); err != nil {
		t.Fatal(err)
	}
	finishSkillControlInput(t, f)
	appendSkillControlInput(t, f, "control-current", "INTERNAL_CONTROL_NOT_MODEL_USER", true)
	run := claimSkillControlInput(t, f)
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
			w.WriteHeader(400)
			return
		}
		tools, _ := request["tools"].([]any)
		if !hasChatToolNamed(tools, "skill") || hasChatToolNamed(tools, "return_conversation_skill") {
			t.Errorf("expected ordinary tool schema, got %#v", tools)
		}
		messages, _ := request["messages"].([]any)
		var modelText strings.Builder
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			content, _ := message["content"].(string)
			modelText.WriteString(content)
		}
		for _, required := range []string{"PROFILE_PROMPT_MUST_SURVIVE", "<recorded_skill_source>", "Create scientific artifacts."} {
			if !strings.Contains(modelText.String(), required) {
				t.Errorf("provider missing %q", required)
			}
		}
		if strings.Contains(modelText.String(), "INTERNAL_CONTROL_NOT_MODEL_USER") {
			t.Error("control recursed into provider replay")
		}
		message := map[string]any{"role": "assistant", "content": "No reusable procedure has been established; no Skill was published."}
		if calls.Add(1) == 1 {
			arguments, _ := json.Marshal(map[string]any{"skill": "customize"})
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "ordinary-read", "type": "function", "function": map[string]any{"name": "skill", "arguments": string(arguments)},
			}}}
		} else if !strings.Contains(modelText.String(), "Inspect evidence; never claim an unpublished draft was published.") ||
			!hasToolResultMessage(messages, requireProviderToolCallID(t, request, "skill", 0), "customize") {
			t.Error("normal runner did not materialize the actual Customize package and its load receipt")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
	}))
	defer provider.Close()
	options := normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
		RunnerID: "skill-control-runner", Endpoint: provider.URL + "/v1/chat/completions", Model: "control-model",
		AllowedTools: []string{"skill"}, DisableMCPDiscovery: true,
		RequestTimeout: 5 * time.Second, PreparationTimeout: 5 * time.Second, MaxToolRounds: 2, MaxAttempts: 1,
		ReplayLimit: 20, OutputLimitBytes: 64 * 1024,
	})
	result, err := f.server.runSessionRunnerChat(withTranscriptRunnerChatRun(context.Background(), run), options, session, entries, run)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || !strings.Contains(result, "no Skill was published") {
		t.Fatalf("ordinary completion is not publication: calls=%d result=%q", calls.Load(), result)
	}
}

func TestConversationSkillContinuationEvidenceKeepsInputCutoffAndToolOwnership(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	originalAttempt := f.claim.Attempt
	for _, phase := range []string{"failed", "outcome_unknown"} {
		payload, _ := json.Marshal(map[string]any{"toolName": "read_file", "toolCallId": "call-" + phase,
			"toolPhase": phase, "status": phase, "toolInput": map[string]any{"file_path": "record.txt"},
			"toolResult": map[string]any{"error": "original-" + phase}})
		if _, _, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
			Claim: f.claim, ClientMessageID: "checkpoint-" + phase, Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}
	finishSkillControlInput(t, f)
	appendSkillControlInput(t, f, "control-admitted", "CURRENT_CONTROL", true)
	run := claimSkillControlInput(t, f)
	appendSkillControlInput(t, f, "future-user", "LATER_REQUEST_MUST_NOT_LEAK", false)
	appendSkillControlInput(t, f, "control-queued", "LATER_CONTROL_MUST_NOT_LEAK", true)
	control, err := f.repo.ClaimedRunnerInputContext(context.Background(), f.claim)
	if err != nil || control == nil {
		t.Fatalf("control=%#v err=%v", control, err)
	}
	source, err := f.server.conversationSkillControlEvidence(context.Background(), run.Transcript, *control)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CURRENT_CONTROL", "LATER_REQUEST_MUST_NOT_LEAK", "LATER_CONTROL_MUST_NOT_LEAK"} {
		if strings.Contains(source.Transcript, forbidden) {
			t.Errorf("source borrowed %q", forbidden)
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(source.Transcript, "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["toolName"] == nil {
			continue
		}
		phase, _ := record["toolPhase"].(string)
		seen[phase] = true
		if record["request_message_id"] != "save-user" || record["runner_attempt"] != float64(originalAttempt) {
			t.Fatalf("tool evidence lost original ownership: %#v", record)
		}
	}
	if !seen["failed"] || !seen["outcome_unknown"] {
		t.Fatalf("missing failed/uncertain outcomes: %s", source.Transcript)
	}
}

func TestConversationSkillContinuationRejectsCancelledAndStaleClaims(t *testing.T) {
	for _, mode := range []string{"cancelled", "superseded"} {
		t.Run(mode, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			loadSkillControlCatalog(t, f)
			finishSkillControlInput(t, f)
			appendSkillControlInput(t, f, "control-fenced", "Save control", true)
			run := claimSkillControlInput(t, f)
			if mode == "cancelled" {
				result, err := f.repo.CancelRunner(context.Background(), transcriptstore.CancelRunnerInput{
					StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ExpectedAttempt: f.claim.Attempt,
					ClientMessageID: "cancel-control", ReasonCode: "user_cancelled",
				})
				if err != nil || !result.Applied {
					t.Fatalf("cancel=%#v err=%v", result, err)
				}
			} else {
				finishSkillControlInput(t, f)
				appendSkillControlInput(t, f, "new-input", "Ordinary next task", false)
				claimSkillControlInput(t, f)
			}
			if _, err := f.server.prepareConversationSkillContinuation(context.Background(), SessionRunnerChatOptions{}, run); err == nil {
				t.Fatal("terminal or superseded claim retained Skill preparation authority")
			}
		})
	}
}
