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

func TestMolecularCompanionCountDifferenceDeliversWithoutReplacingAnswer(t *testing.T) {
	f, session, run, original := molecularCompanionCountCompletionFixture(t)
	commits, err := f.server.sessionRunnerArtifactCommitReferences(t.Context(), run)
	if err != nil || len(commits) != 2 {
		t.Fatalf("original immutable pair: commits=%+v err=%v", commits, err)
	}
	counts := map[string]int{}
	validation, err := inspectScientificArtifactSelection(t.Context(), f.store, f.stream.ProjectID, commits, original,
		func(ctx context.Context, reader io.Reader, format string) (scientificArtifactValidation, error) {
			result, err := f.server.validateMoleculeArtifact(ctx, reader, format)
			counts[format] = result.ParsedCount
			return result, err
		})
	if err != nil || counts["sdf"] != 2 || counts["smi"] != 1 || len(validation.Selection) != 2 || len(validation.Unavailable) != 0 {
		t.Fatalf("pair was not actually parsed with its original identities: counts=%v validation=%+v err=%v", counts, validation, err)
	}
	if len(validation.Failures) != 0 {
		t.Errorf("valid conformer/distinct-molecule counts were treated as invalid bytes: %v", validation.Failures)
	}
	model := &artifactReferenceRepairModel{content: original, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(t.Context(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{
			Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}},
		}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, original) ||
		strings.Count(result.FinalMessage.Content, "Result quality is unverified") != 1 {
		t.Fatalf("aggregate count replaced or blocked the original final: calls=%d text=%q err=%v", model.calls, result.FinalMessage.Content, err)
	}
	if len(result.Messages) == 0 || result.Messages[len(result.Messages)-1].Content != result.FinalMessage.Content ||
		strings.Contains(result.FinalMessage.Content, "Chemical validation was not performed") {
		t.Fatal("canonical final diverged or real parsing was mislabeled as unavailable")
	}
}

func TestRecoveredQualityConditionUsesTypedFindingsAndKeepsIntegrityFailures(t *testing.T) {
	quality := "machine_validation_false_check:validation.json path=checks.valid"
	countDifference := "molecule_set_record_count_mismatch:molecules sdf=2 smiles=1"
	for _, test := range []struct {
		name      string
		reference transcriptstore.RunnerReferenceCondition
		advisory  bool
	}{
		{"quality", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{quality}}, true},
		{"source coverage", transcriptstore.RunnerReferenceCondition{CrossArtifactFailures: []string{"source_claim_missing_attested_excerpt:source_evidence.json source=s1"}}, true},
		{"unsupported citation", transcriptstore.RunnerReferenceCondition{UnsupportedCitations: []string{"doi:10.1000/example"}}, true},
		{"typed aggregate molecule count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{countDifference}}, true},
		{"aggregate plus per-file count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{countDifference, "record_count_mismatch"}}, false},
		{"aggregate plus invalid structure", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{countDifference, "invalid_sdf_records"}}, false},
		{"aggregate plus unknown scientific finding", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{countDifference, "unknown_molecule_failure"}}, false},
		{"aggregate plus missing deliverable", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{countDifference}, MissingRequiredDeliverables: []string{"report.csv"}}, false},
		{"malformed aggregate count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=two smiles=1"}}, false},
		{"empty aggregate stem", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch: sdf=2 smiles=1"}}, false},
		{"noncanonical aggregate count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=02 smiles=1"}}, false},
		{"negative aggregate count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=-2 smiles=1"}}, false},
		{"overflowing aggregate count", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=9223372036854775808 smiles=1"}}, false},
		{"equal aggregate counts", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=2 smiles=2"}}, false},
		{"trailing aggregate diagnostic", transcriptstore.RunnerReferenceCondition{InvalidScientificArtifacts: []string{"molecule_set_record_count_mismatch:molecules sdf=2 smiles=1 invalid_sdf_records"}}, false},
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

// Use the existing installed managed parser, never synthetic successful parser
// replies or environment provisioning. Both records describe ethanol with
// different coordinates, while the SMILES file represents that molecule once.
func molecularCompanionCountCompletionFixture(t *testing.T) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, string) {
	t.Helper()
	t.Setenv("X_SCIENCE_TEST_PROVISION_SCIENTIFIC_RUNTIME", "0")
	f := newAgentSaveArtifactsFixtureWithKernelManager(t, realManagedScientificKernelManagerForServerTest(t))
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical intent: found=%t err=%v", found, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(validServerEthanolSDF, "1.2990    0.7500", "1.1000    0.9000", 1)
	for _, file := range []struct{ path, body string }{
		{"out/molecules.sdf", validServerEthanolSDF + second},
		{"out/molecules.smi", validServerEthanolSMILES},
	} {
		write := writeAgentSaveArtifactsFile(t, f.projectPath, file.path, file.body)
		f.saveExecution(t, f.identity.access, f.projectPath, file.path, 1, write)
	}
	input := map[string]any{"files": []any{"out/molecules.sdf", "out/molecules.smi"}, "language": "python",
		"environment": "x-science-python", "human_description": "Save the existing molecular coordinate and SMILES records"}
	saved, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "molecule-count-save", input), f.identity, "molecule-count-save", input)
	if err != nil || saved["errors"] != nil {
		t.Fatalf("real parser did not save the original pair: saved=%+v err=%v", saved, err)
	}
	artifacts := agentSaveArtifactResults(t, saved)
	if len(artifacts) != 2 || len(anySliceValue(saved["warnings"])) != 0 {
		t.Fatalf("real parser did not save the two valid original files: saved=%+v err=%v", saved, err)
	}
	content := "The original molecular coordinate and SMILES files are preserved."
	for _, result := range artifacts {
		versionID := stringValue(result["version_id"])
		artifact, _, reader, found, err := f.store.OpenArtifactVersionContent(versionID)
		if reader != nil {
			_ = reader.Close()
		}
		if err != nil || !found {
			t.Fatalf("saved original version unavailable: found=%t err=%v", found, err)
		}
		content += "\n[" + artifact.Name + "]({{artifact:" + versionID + "}})."
	}
	return f, sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}}, run, content
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
	return qualityArtifactCompletionFixture(t, "validation.json", `{"checks":{"valid":false},"status":"failed","failures":["quality threshold not met"]}`)
}

func qualityArtifactCompletionFixture(t *testing.T, name, original string) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun, string, string) {
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
	path := "out/" + name
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, original)
	f.saveExecution(t, f.identity.access, f.projectPath, "quality-report-write", 1, write)
	input := map[string]any{"files": []any{path}, "language": "text", "human_description": "Save the observed quality report"}
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
