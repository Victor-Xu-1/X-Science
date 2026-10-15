package server

import (
	"context"
	"io"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestCompletionDeliversFailedQualityReportWithVisibleNotice(t *testing.T) {
	f, session, run, versionID, original := qualityReportCompletionFixture(t)
	model := &artifactReferenceRepairModel{content: "The recorded check failed; see the preserved report.", testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(context.Background(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{
			Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}},
		}, run, nil)
	if err != nil || model.calls != 1 || !strings.Contains(result.FinalMessage.Content, "Result quality is unverified") {
		t.Fatalf("report delivery still blocks or hides the limitation: calls=%d result=%q error=%v", model.calls, result.FinalMessage.Content, err)
	}
	if !strings.HasPrefix(result.FinalMessage.Content, model.content) || len(result.Messages) == 0 ||
		result.Messages[len(result.Messages)-1].Content != result.FinalMessage.Content {
		t.Fatal("public answer and authoritative candidate diverged")
	}
	_, _, reader, found, err := f.store.OpenArtifactVersionContent(versionID)
	if err != nil || !found {
		t.Fatalf("original report unavailable: found=%t error=%v", found, err)
	}
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	if err != nil || string(actual) != original {
		t.Fatal("failed quality report was rewritten into a pass")
	}
}

func TestRecoveredQualityConditionUsesTypedFindingsAndKeepsIntegrityFailures(t *testing.T) {
	quality := "machine_validation_false_check:validation.json path=checks.valid"
	for _, test := range []struct {
		name      string
		reference transcriptstore.RunnerReferenceCondition
		advisory  bool
	}{
		{"quality", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{quality}}, true},
		{"source coverage", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"source_claim_missing_attested_excerpt:source_evidence.json source=s1"}}, true},
		{"unsupported citation", transcriptstore.RunnerReferenceCondition{UnsupportedCitations: []string{"doi:10.1000/example"}}, true},
		{"empty", transcriptstore.RunnerReferenceCondition{}, false},
		{"unresolved count", transcriptstore.RunnerReferenceCondition{UnresolvedArtifacts: 1, CrossArtifactFailures: []string{quality}}, false},
		{"unresolved identity", transcriptstore.RunnerReferenceCondition{UnresolvedArtifactReferences: []string{"missing"}, CrossArtifactFailures: []string{quality}}, false},
		{"required output", transcriptstore.RunnerReferenceCondition{MissingRequiredDeliverables: []string{"report.csv"}, CrossArtifactFailures: []string{quality}}, false},
		{"local file", transcriptstore.RunnerReferenceCondition{MissingLocalArtifacts: []string{"input.csv"}, CrossArtifactFailures: []string{quality}}, false},
		{"invalid structure", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"invalid_sdf"}, CrossArtifactFailures: []string{quality}}, false},
		{"invalid reference", transcriptstore.RunnerReferenceCondition{InvalidReferenceArtifacts: []string{"wrong project"}, CrossArtifactFailures: []string{quality}}, false},
		{"invalid research bytes", transcriptstore.RunnerReferenceCondition{InvalidResearchArtifacts: []string{"invalid_json"}, CrossArtifactFailures: []string{quality}}, false},
		{"malformed reference", transcriptstore.RunnerReferenceCondition{MalformedArtifactReferences: []string{"bad placeholder"}, CrossArtifactFailures: []string{quality}}, false},
		{"corrupt report", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{quality, "machine_validation_invalid_json:validation.json"}}, false},
		{"unknown", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{quality, "unrecognized_integrity_failure:report.md"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := (&sessionRunnerReferenceIntegrityError{
				// This harmless display text must not override the typed body.
				UnsupportedCitations: []string{"doi:10.1000/example"},
			}).runnerCorrection()
			cause.Condition.Reference = &test.reference
			got, err := (&Server{}).recoveredCompletionCorrectionIsAdvisory("frame", &sessionRunnerChatRun{},
				recoveredRunnerCorrection{ReasonCode: cause.ReasonCode, Detail: cause.Detail, Condition: cause.Condition})
			if err != nil || got != test.advisory {
				t.Fatalf("advisory=%t want=%t error=%v", got, test.advisory, err)
			}
		})
	}
}

func TestCompletionQualityNoticeSharesIdempotentLocalizedPresentation(t *testing.T) {
	quality := &sessionRunnerCompletionAdvisory{Code: "result_quality_unverified"}
	scene := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryStructurePreview}
	for _, language := range []string{"", "en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			text := appendSessionRunnerCompletionAdvisories("Measured result: 12.5.", language, quality, scene, quality)
			want := "Result quality is unverified"
			if sessionRunnerRequiresChinese(language) {
				want = "结果质量尚未验证"
			}
			if strings.Count(text, want) != 1 || appendSessionRunnerCompletionAdvisories(text, language, quality, scene) != text {
				t.Fatalf("notice missing or repeated: %q", text)
			}
			if got := appendSessionRunnerCompletionAdvisories(" \n", language, quality); got != " \n" {
				t.Fatal("a quality notice fabricated an empty answer")
			}
		})
	}
}

func qualityReportCompletionFixture(t *testing.T) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, string, string) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(context.Background(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical intent: found=%t error=%v", found, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	original := `{"checks":{"valid":false},"status":"failed","failures":["quality threshold not met"]}`
	write := writeAgentSaveArtifactsFile(t, f.projectPath, "out/validation.json", original)
	f.saveExecution(t, f.identity.access, f.projectPath, "quality-report-write", 1, write)
	input := map[string]any{"files": []any{"out/validation.json"}, "language": "text", "human_description": "Save the observed quality report"}
	saved, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "quality-report-save", input), f.identity, "quality-report-save", input)
	if err != nil || saved["errors"] != nil {
		t.Fatalf("save original report: %#v %v", saved, err)
	}
	artifacts := agentSaveArtifactResults(t, saved)
	if len(artifacts) != 1 {
		t.Fatalf("saved report count=%d", len(artifacts))
	}
	session := sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}}
	return f, session, run, stringValue(artifacts[0]["version_id"]), original
}
