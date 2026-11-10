package server

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func assertTemplateQualityCommittedWarning(t *testing.T, f *agentSaveArtifactsFixture, result map[string]any, path string) map[string]any {
	t.Helper()
	artifacts := agentSaveArtifactResults(t, result)
	if result["errors"] != nil || len(artifacts) != 1 {
		t.Fatalf("template quality was not saved: %#v", result)
	}
	a := artifacts[0]
	_, version, found, err := f.store.GetArtifactVersionMetadata(stringValue(a["version_id"]))
	if err != nil || !found {
		t.Fatalf("warning has no committed version: found=%t err=%v", found, err)
	}
	for _, raw := range anySliceValue(result["warnings"]) {
		warning := mapValue(raw)
		if warning["path"] == path && warning["blocking"] == false && warning["quality_advisory"] == true &&
			warning["artifact_id"] == a["artifact_id"] && warning["version_id"] == a["version_id"] && warning["sha256"] == version.ContentSHA256 &&
			strings.Contains(strings.Join(stringArrayValue(warning["findings"]), ","), "unresolved_template_marker:") {
			return a
		}
	}
	t.Fatalf("quality warning lacks committed identity: %#v", result["warnings"])
	return nil
}

func TestTemplateQualitySaveWarnsOnlyAfterCanonicalCommit(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	const path, original = "out/template_report.md", "# Report\n{{ display.value }}\n"
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, original)
	f.saveExecution(t, f.identity.access, f.projectPath, "template-quality-write", 1, write)
	input := map[string]any{"files": []any{path}, "language": "text", "human_description": "Preserve report with quality observations"}
	result, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "template-quality-save", input), f.identity, "template-quality-save", input)
	if err != nil || result["errors"] != nil {
		t.Fatalf("template quality blocked preservation: result=%#v err=%v", result, err)
	}
	artifacts := agentSaveArtifactResults(t, result)
	if len(artifacts) != 1 {
		t.Fatalf("saved artifacts=%#v", artifacts)
	}
	a := artifacts[0]
	_, version, reader, found, err := f.store.OpenArtifactVersionContent(stringValue(a["version_id"]))
	if err != nil || !found {
		t.Fatalf("committed version missing: found=%t err=%v", found, err)
	}
	defer reader.Close()
	bytes, err := io.ReadAll(reader)
	if err != nil || string(bytes) != original {
		t.Fatalf("save rewrote original report: %v", err)
	}
	bound := false
	for _, raw := range anySliceValue(result["warnings"]) {
		warning := mapValue(raw)
		if strings.Contains(strings.Join(stringArrayValue(warning["findings"]), ","), "unresolved_template_marker:") {
			bound = warning["blocking"] == false && warning["quality_advisory"] == true &&
				warning["artifact_id"] == a["artifact_id"] && warning["version_id"] == a["version_id"] && warning["sha256"] == version.ContentSHA256
		}
	}
	if !bound {
		t.Fatalf("quality warning lacks actual committed identity: %#v", result["warnings"])
	}
	// The unchanged association path must bind its warning to the same durable
	// version instead of inventing another successful save or dropping the notice.
	replayed, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "template-quality-replay", input), f.identity, "template-quality-replay", input)
	if err != nil {
		t.Fatal(err)
	}
	replay := assertTemplateQualityCommittedWarning(t, f, replayed, path)
	if replay["version_id"] != a["version_id"] || replay["unchanged"] != true {
		t.Fatalf("unchanged association altered the report: %#v", replay)
	}
}

func TestTemplateQualitySaveKeepsMalformedAndDelimitedFailuresHard(t *testing.T) {
	for _, tc := range []struct{ name, path, content, code string }{
		{"malformed reference", "out/malformed.md", "{{artifact:not-an-artifact-id}}", "unresolved_template_marker"},
		{"template and malformed reference", "out/mixed.md", "{{ display.value }} {{artifact:bad}}", "unresolved_template_marker"},
		{"Jinja and malformed reference", "out/jinja.md", "{% render value %} {{artifact:bad}}", "unresolved_template_marker"},
		{"template and broken CSV", "out/broken.csv", "left,right\n{{ value }},x,extra\n", "invalid_delimited_artifact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAgentSaveArtifactsFixture(t)
			write := writeAgentSaveArtifactsFile(t, f.projectPath, tc.path, tc.content)
			f.saveExecution(t, f.identity.access, f.projectPath, "hard-template-write", 1, write)
			input := map[string]any{"files": []any{tc.path}, "language": "text", "human_description": "Validate current bytes"}
			result, err := f.server.executeAgentSaveArtifacts(f.toolContext(t, "hard-template-save", input), f.identity, "hard-template-save", input)
			if !errors.Is(err, errAgentSaveArtifactsNoResults) || len(anySliceValue(result["artifacts"])) != 0 || len(anySliceValue(result["warnings"])) != 0 {
				t.Fatalf("hard failure became warning success: %#v %v", result, err)
			}
			failures := agentSaveArtifactFailures(t, result)
			if len(failures) != 1 || failures[0]["code"] != tc.code {
				t.Fatalf("hard validator lost its actual failure: %#v", failures)
			}
			assertAgentSaveArtifactsNoCanonicalWrites(t, f)
		})
	}
}

// The setup is the already exercised native save lifecycle from
// TestCompletionTypedAdvisoriesPersistAcrossFreshLiveHistoryAndReplay.
func templateQualityProjectionFixture(t *testing.T) (*agentSaveArtifactsFixture, sessionstore.Session, *sessionRunnerChatRun) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("canonical intent: found=%t err=%v", found, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	const path = "out/typed_validation.json"
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, `{"checks":{"valid":false},"status":"failed"}`)
	f.saveExecution(t, f.identity.access, f.projectPath, "quality-projection-write", 1, write)
	input := map[string]any{"files": []any{path}, "language": "text", "human_description": "Preserve observed validation report"}
	payload, _ := json.Marshal(map[string]any{"toolName": "save_artifacts", "toolPhase": "start", "status": "running", "toolCallId": "quality-projection-save", "toolInput": input})
	_, source, _, err := f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
		ClientMessageID: "quality-projection-start", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: payload, Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.server.executeAgentSaveArtifacts(withTranscriptArtifactRun(t.Context(), run.Transcript, source.EventID), f.identity, "quality-projection-save", input)
	if err != nil || saved["errors"] != nil {
		t.Fatalf("real save lifecycle: %#v %v", saved, err)
	}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "quality-projection-completed", map[string]any{"toolName": "save_artifacts", "toolPhase": "completed", "status": "completed", "toolCallId": "quality-projection-save", "toolInput": input, "toolResult": saved})
	return f, sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}}, run
}

// Only the actual final candidate adds a report-template observation.
func TestTemplateQualityFreshCompletionPreservesFindingAndVisibleNotice(t *testing.T) {
	f, session, run := templateQualityProjectionFixture(t)
	model := &artifactReferenceRepairModel{content: "The preserved report contains {{ display.value }}.", testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(t.Context(), session,
		agentruntime.Engine{Model: model}, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil || model.calls != 1 || !strings.HasPrefix(result.FinalMessage.Content, model.content) || !strings.Contains(result.FinalMessage.Content, "Result quality is unverified") {
		t.Fatalf("template observation replaced or blocked the actual candidate: calls=%d result=%q err=%v", model.calls, result.FinalMessage.Content, err)
	}
	presentation := run.completionAdvisorySnapshot()
	wire := presentation.envelope(result.FinalMessage.Content, f.claim)
	found := false
	if wire != nil {
		for _, item := range wire.Items {
			for _, finding := range item.Findings {
				found = found || strings.HasPrefix(finding, "unresolved_template_marker:")
			}
		}
	}
	if !found {
		t.Fatal("completion discarded its actual template-check observation")
	}
	final, err := f.server.persistTranscriptRunnerFinalization(t.Context(), run.Transcript, transcriptRunnerFinalization{
		Content: result.FinalMessage.Content, Status: "completed", Detail: "runner chat completed", SegmentOrdinal: 1,
		CompletionAdvisories: presentation,
	})
	if err != nil {
		t.Fatal(err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(final.Assistant.PayloadJSON, &canonical); err != nil {
		t.Fatal(err)
	}
	if saved := sessionRunnerCompletionAdvisoriesFromPayload(canonical, f.claim.Attempt); saved == nil ||
		!strings.Contains(string(mustJSON(t, saved)), "unresolved_template_marker:") {
		t.Fatal("atomic finalization dropped the template observation")
	}
	history, handled, err := f.server.loadTranscriptWebHistory(t.Context(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || !handled || !strings.Contains(string(mustJSON(t, history)), "completionAdvisories") {
		t.Fatalf("history lost the authoritative quality warning: handled=%t err=%v", handled, err)
	}
	if err := f.server.runTranscriptWebDeliveryCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	streamed := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		streamed = streamed || event.Type == "message.stream" && event.Payload["completion_advisories"] != nil
	}
	if !streamed {
		t.Fatal("stream dropped the persisted quality warning")
	}
	replay, err := f.server.loadTranscriptRunnerReplay(t.Context(), run.Transcript, 80, 80)
	if err != nil {
		t.Fatal(err)
	}
	quoted := false
	for _, entry := range replay {
		text := stringValue(entry.Message["text"])
		quoted = quoted || strings.Contains(text, `"host_completion_observations"`) && strings.Contains(text, "unresolved_template_marker:")
	}
	if !quoted {
		t.Fatal("provider replay lost the quoted host observation")
	}
}
