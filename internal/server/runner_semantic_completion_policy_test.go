package server

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/skills"
)

func TestSemanticTaskWordingDoesNotBlockFinalAnswer(t *testing.T) {
	check := func(t *testing.T, task, content string, wantAdvisory bool) {
		t.Helper()
		model := &artifactReferenceRepairModel{content: content, testing: t}
		result, err := semanticCompletionFixture(t, context.Background(), task, model)
		if err != nil {
			t.Fatalf("text-derived condition blocked the answer: %v", err)
		}
		if model.calls != 1 {
			t.Fatalf("task wording replayed the model: calls=%d", model.calls)
		}
		if !strings.HasPrefix(result.FinalMessage.Content, model.content) {
			t.Fatal("model explanation was not retained in the final answer")
		}
		if got := strings.Contains(result.FinalMessage.Content, "Execution details are unverified"); got != wantAdvisory {
			t.Fatalf("heuristic advisory=%t, want=%t", got, wantAdvisory)
		}
		if !wantAdvisory && result.FinalMessage.Content != model.content {
			t.Fatal("unrelated wording added content to the model answer")
		}
		if len(result.Messages) == 0 {
			t.Fatal("authoritative final message is missing")
		}
		if result.Messages[len(result.Messages)-1].Content != result.FinalMessage.Content {
			t.Fatal("authoritative final diverges from the returned final answer")
		}
	}
	for _, test := range []struct {
		task         string
		wantAdvisory bool
	}{
		{"Explain web_search; do not execute it.", true},
		{"Do not use search_skills. Explain the available concept only.", true},
		{"Describe manage_packages without installing anything.", true},
		{"Explain how to use a skill; do not invoke one.", false},
		{"Explain an actual MCP method without calling it.", true},
		{"Explain the phrase rerun analysis; no operation should execute.", true},
		{"Report the first failure code if a failure was observed.", true},
		{"Use web_search to inspect the question.", true},
	} {
		t.Run(test.task, func(t *testing.T) {
			check(t, test.task, "This is an explanation. No new operation was executed.", test.wantAdvisory)
		})
	}
	t.Run("ordinary wording", func(t *testing.T) {
		check(t, "Explain two plus two.", "The arithmetic result is four.", false)
	})
}

func semanticCompletionFixture(t *testing.T, ctx context.Context, task string, model agentruntime.ModelClient) (agentruntime.RunResult, error) {
	t.Helper()
	server := &Server{workspaceStore: openRunnerArtifactCompletionStore(t)}
	run := &sessionRunnerChatRun{TaskIntent: task, ResponseLanguage: "en", VerificationExplicitlyDisabled: true}
	return server.runVerifiedSessionAgent(ctx,
		sessionstore.Session{ID: "semantic-frame", Project: &sessionstore.Project{ID: "project-a"}},
		SessionRunnerChatOptions{}, agentruntime.Engine{Model: model},
		agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: task}}},
		sessionRunnerTaskContract{}, run, nil)
}

func TestAttachmentReaderPreferenceDoesNotChangeOperationAdmission(t *testing.T) {
	for _, task := range []string{
		"Read the attached document.",
		"Do not use manage_packages; discuss the attachment only.",
		"Use manage_packages for a separately authorized environment.",
	} {
		t.Run(task, func(t *testing.T) {
			run := &sessionRunnerChatRun{TaskIntent: task}
			run.setInputAttachmentReaders([]sessionRunnerInputAttachmentReader{{
				VersionID: "version-doc", Filename: "input.docx", Reader: "builtin_docx", EquivalentPackages: []string{"python-docx"},
			}})
			gateway := serverAgentRuntimeToolGateway{server: &Server{skillCatalog: &skills.Catalog{}}, taskRun: run}
			if got := gateway.agentRuntimeSkillExecutionContractPreflight("manage_packages",
				map[string]any{"mode": "install", "packages": []any{"python-docx"}}); got != nil {
				t.Fatalf("reader preference became an execution prerequisite: %#v", got)
			}
			if run.InputAttachmentReaders[0].Read {
				t.Fatal("removing a preference gate fabricated a read receipt")
			}
		})
	}
}

func TestSemanticDisclosureKeepsReceiptAndFinalIdentityBoundaries(t *testing.T) {
	for _, condition := range []*transcriptstore.RunnerCorrectionCondition{
		{Schema: "unknown", ReasonCode: "completion_review_correction_required", Review: &transcriptstore.RunnerReviewCondition{}},
		{Schema: transcriptstore.RunnerCorrectionConditionSchema, ReasonCode: "completion_review_correction_required",
			Review: &transcriptstore.RunnerReviewCondition{}, Reference: &transcriptstore.RunnerReferenceCondition{UnresolvedArtifacts: 1}},
		{Schema: transcriptstore.RunnerCorrectionConditionSchema, ReasonCode: "artifact_reference_correction_required",
			Reference: &transcriptstore.RunnerReferenceCondition{UnresolvedArtifacts: 1}},
	} {
		if sessionRunnerSemanticReviewIsAdvisory(recoveredRunnerCorrection{ReasonCode: "completion_review_correction_required", Condition: condition}) {
			t.Fatalf("invalid, mixed or foreign condition became semantic advice: %#v", condition)
		}
	}
	messages := []agentruntime.Message{
		toolRound("failed-search", "web_search"),
		{Role: "tool", ToolCallID: "failed-search", Content: `{"ok":false,"code":"source_unavailable"}`},
	}
	before := append([]agentruntime.Message(nil), messages...)
	if sessionRunnerSuccessfulToolExecutionIndex(messages)["web_search"] != 0 || !reflect.DeepEqual(messages, before) {
		t.Fatal("failed execution was relabelled as successful")
	}
	model := &artifactReferenceRepairModel{content: "See [result]({{artifact:" + unresolvedArtifactVersionID + "}})", testing: t}
	_, err := semanticCompletionFixture(t, context.Background(), "Explain web_search.", model)
	var integrity *sessionRunnerReferenceIntegrityError
	if !errors.As(err, &integrity) || integrity.UnresolvedArtifacts != 1 {
		t.Fatalf("real missing artifact identity became advisory: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := semanticCompletionFixture(t, ctx, "Explain web_search.", model); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation became completion: %v", err)
	}
	advisory := &sessionRunnerCompletionAdvisory{Code: "task_expectations_unverified"}
	content := appendSessionRunnerCompletionAdvisories("No successful action was recorded.", "en", advisory)
	if !strings.Contains(content, "Execution details are unverified") ||
		appendSessionRunnerCompletionAdvisories(content, "en", advisory) != content ||
		appendSessionRunnerCompletionAdvisories(" \n", "en", advisory) != " \n" {
		t.Fatal("notice missing, duplicated, or manufactured an empty answer")
	}
	if translated := appendSessionRunnerCompletionAdvisories("未记录到成功操作。", "zh", advisory); !strings.Contains(translated, "执行细节尚未核实") {
		t.Fatal("explicit Chinese preference was not preserved")
	}
	f := newAgentSaveArtifactsFixture(t)
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	payload := map[string]any{"text": content, "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, "")}
	if err := f.server.appendTranscriptAssistantEvent(context.Background(), authority, "semantic-final", payload); err != nil {
		t.Fatal(err)
	}
	if err := f.server.appendTranscriptAssistantEvent(context.Background(), authority, "semantic-final", payload); err != nil {
		t.Fatalf("identical final retry was not idempotent: %v", err)
	}
	payload["text"] = content + " changed after publication"
	if err := f.server.appendTranscriptAssistantEvent(context.Background(), authority, "semantic-final", payload); !errors.Is(err, transcriptstore.ErrEventConflict) {
		t.Fatalf("published answer could be overwritten: %v", err)
	}
}
