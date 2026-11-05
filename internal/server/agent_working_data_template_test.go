package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestAgentSaveWorkingDataHTMLKeepsRawBytesAcrossReplayAndCompletion(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	const reportPath, sourcePath = "out/report.md", "sources/page.html"
	reportID := agentSavedArtifactID(fixture.stream.UID, reportPath)
	// Client templates and JavaScript operators are literal source bytes. A
	// project-resolvable artifact-shaped string is also data, not a report link.
	source := `<html><script>const path="reports/upstream.csv";const template="{{ client.value }}";const close="{{/}}";` +
		`const ref="{{artifact:` + reportID + `}}";function shift(x){return x >> 1;}</script></html>`
	reportWrite := writeAgentSaveArtifactsFile(t, fixture.projectPath, reportPath, "# Findings\n\nThe source was retained for audit.\n")
	sourceWrite := writeAgentSaveArtifactsFile(t, fixture.projectPath, sourcePath, source)
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "execution-retention-template", 1, reportWrite, sourceWrite)
	input := map[string]any{
		"files": []any{reportPath, sourcePath}, "language": "text",
		"destination":       map[string]any{reportPath: "snapshot", "sources/": "working_data"},
		"human_description": "Save the rendered report and unchanged source evidence",
	}
	saved, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "save-retention-template", input), fixture.identity, "save-retention-template", input,
	)
	if err != nil || len(agentSaveArtifactResults(t, saved)) != 2 || len(anySliceValue(saved["errors"])) != 0 {
		t.Fatalf("mixed report/source save=%#v err=%v", saved, err)
	}
	artifacts := agentSaveArtifactResults(t, saved)
	report, raw := artifacts[0], artifacts[1]
	if report["publication_state"] != "draft" || raw["publication_state"] != "working_data" || raw["artifact_ref"] != nil {
		t.Fatalf("retention projection=%#v", artifacts)
	}
	if warnings := fmt.Sprint(saved["warnings"]); strings.Contains(warnings, "unresolved_template_marker") || strings.Contains(warnings, "missing_artifact_reference") {
		t.Fatalf("source evidence reached report-semantic batch correction: %#v", saved["warnings"])
	}
	assertWorkingDataSourceBytes(t, fixture, stringValue(raw["version_id"]), sourcePath, source)
	digest := sha256.Sum256([]byte(source))
	if raw["checksum"] != hex.EncodeToString(digest[:]) {
		t.Fatalf("raw source checksum=%v want=%x", raw["checksum"], digest)
	}

	// No destination on replay must inherit the same canonical retention mode,
	// rather than treating an existing working source as a new report.
	replay := map[string]any{"files": []any{sourcePath}, "language": "text", "human_description": "Retain unchanged source"}
	replayed, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "replay-retention-template", replay), fixture.identity, "replay-retention-template", replay,
	)
	if err != nil || len(agentSaveArtifactResults(t, replayed)) != 1 {
		t.Fatalf("working-source replay=%#v err=%v", replayed, err)
	}
	if replayRaw := agentSaveArtifactResults(t, replayed)[0]; replayRaw["unchanged"] != true ||
		replayRaw["version_id"] != raw["version_id"] || replayRaw["retention"] != "working_data" {
		t.Fatalf("working-source replay changed identity or retention: %#v", replayRaw)
	}

	// Explicitly promoting those bytes to a rendered report must still fail
	// the existing template gate, without changing their stored retention.
	promote := map[string]any{
		"files": []any{sourcePath}, "language": "text", "human_description": "Publish a rendered source page",
		"destination": map[string]any{sourcePath: "snapshot"},
	}
	promoted, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "promote-retention-template", promote), fixture.identity, "promote-retention-template", promote,
	)
	if !errors.Is(err, errAgentSaveArtifactsNoResults) {
		t.Fatalf("unrendered promotion=%#v err=%v", promoted, err)
	}
	failures := agentSaveArtifactFailures(t, promoted)
	if len(failures) != 1 {
		t.Fatalf("promotion failures=%#v", failures)
	}
	assertAgentSaveArtifactFailure(t, failures[0], sourcePath, "unresolved_template_marker")
	if retention, found, err := fixture.store.ArtifactRetentionMode(stringValue(raw["artifact_id"])); err != nil || !found || retention != "working_data" {
		t.Fatalf("failed promotion retention=%q found=%t err=%v", retention, found, err)
	}

	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	commits, err := fixture.server.sessionRunnerArtifactCommitReferences(context.Background(), run)
	if err != nil || len(commits) != 1 || commits[0].VersionID != report["version_id"] {
		t.Fatalf("terminal deliverables included source data: %#v err=%v", commits, err)
	}
	if err := fixture.server.publishSessionRunnerCompletionArtifacts(context.Background(),
		sessionstore.Session{ID: fixture.stream.SessionID, Project: &sessionstore.Project{ID: fixture.stream.ProjectID}}, run, commits); err != nil {
		t.Fatal(err)
	}
	// The store retains both records. The conversation API, rather than the
	// storage listing, owns exclusion of internal working data from delivery.
	request := httptest.NewRequest(http.MethodPost, "/api/conversation/artifacts", strings.NewReader(
		fmt.Sprintf(`{"version_ids":[%q,%q]}`, report["version_id"], raw["version_id"])))
	request.Header.Set("X-Science-User-Id", fixture.stream.OwnerID)
	response := httptest.NewRecorder()
	frame, found, err := fixture.store.GetCompatibilityFrame(fixture.stream.FrameID)
	if err != nil || !found {
		t.Fatalf("conversation projection frame found=%t err=%v", found, err)
	}
	fixture.server.handleWebConversationArtifacts(response, request, frame)
	var projection []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil || response.Code != http.StatusOK || len(projection) != 1 {
		t.Fatalf("terminal artifact projection status=%d err=%v body=%s", response.Code, err, response.Body.String())
	}
	visible := anySliceValue(mapValue(projection[0]["payload"])["files"])
	if len(visible) != 1 || mapValue(visible[0])["version_id"] != report["version_id"] || mapValue(visible[0])["is_intermediate"] != false {
		t.Fatalf("terminal visible artifacts=%#v", visible)
	}
	assertWorkingDataSourceBytes(t, fixture, stringValue(raw["version_id"]), sourcePath, source)
}

func assertWorkingDataSourceBytes(t *testing.T, fixture *agentSaveArtifactsFixture, versionID, sourcePath, want string) {
	t.Helper()
	_, _, reader, found, err := fixture.store.OpenArtifactVersionContent(versionID)
	if err != nil || !found {
		t.Fatalf("raw source version found=%t err=%v", found, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(data) != want {
		t.Fatalf("raw source bytes changed: read=%v close=%v equal=%t", readErr, closeErr, string(data) == want)
	}
	data, err = os.ReadFile(filepath.Join(fixture.projectPath, filepath.FromSlash(sourcePath)))
	if err != nil || string(data) != want {
		t.Fatalf("workspace source changed: err=%v equal=%t", err, string(data) == want)
	}
}

func TestRunnerTemplatePolicyUsesCanonicalRetentionOnly(t *testing.T) {
	store := openRunnerArtifactCompletionStore(t)
	artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "retained-source", ProjectID: "project-a", Name: "source.html", Kind: "text/html",
		Content: []byte(`<html><script>const template="{{ client.value }}";</script></html>`), CreatedBy: "runner",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceStore: store}
	commits := []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifact.ID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}}
	for _, test := range []struct {
		mode string
		want []string
	}{
		{mode: "", want: []string{"unresolved_template_marker:source.html"}},
		{mode: "working_data", want: []string{}},
		{mode: "snapshot", want: []string{"unresolved_template_marker:source.html"}},
	} {
		t.Run("retention="+test.mode, func(t *testing.T) {
			if test.mode != "" {
				if err := store.SetArtifactRetentionMode(context.Background(), artifact.ID, artifact.ProjectID, "local", test.mode); err != nil {
					t.Fatal(err)
				}
			}
			failures, err := server.validateSessionRunnerCrossArtifactConsistency(context.Background(), artifact.ProjectID, commits)
			if err != nil || !reflect.DeepEqual(failures, test.want) {
				t.Fatalf("template findings=%#v want=%#v err=%v", failures, test.want, err)
			}
		})
	}
	if err := store.SetArtifactRetentionMode(context.Background(), artifact.ID, artifact.ProjectID, "local", "working_data"); err != nil {
		t.Fatal(err)
	}
	failures, err := server.validateSessionRunnerCrossArtifactConsistency(context.Background(), artifact.ProjectID, commits, "Summary: {{ unfinished }}")
	if want := []string{"unresolved_template_marker:final_response.md"}; err != nil || !reflect.DeepEqual(failures, want) {
		t.Fatalf("final response lost strict policy: findings=%#v err=%v", failures, err)
	}
	t.Run("working_data_keeps_machine_validation", func(t *testing.T) {
		artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
			ArtifactID: "retained-validation", ProjectID: "project-a", Name: "validation.json", Kind: "application/json",
			Content: []byte(`{invalid-json}`), CreatedBy: "runner",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetArtifactRetentionMode(context.Background(), artifact.ID, artifact.ProjectID, "local", "working_data"); err != nil {
			t.Fatal(err)
		}
		commits := []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifact.ID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}}
		failures, err := server.validateSessionRunnerCrossArtifactConsistency(context.Background(), artifact.ProjectID, commits)
		if want := []string{"machine_validation_invalid_json:validation.json"}; err != nil || !reflect.DeepEqual(failures, want) {
			t.Fatalf("working data lost machine integrity: findings=%#v err=%v", failures, err)
		}
	})
}

func TestAgentSaveWorkingDataStillRejectsInvalidJSON(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	const path = "sources/invalid.json"
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, path, "{invalid-json}\n")
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "execution-invalid-working-json", 1, write)
	input := map[string]any{
		"files": []any{path}, "language": "text", "human_description": "Retain source data",
		"destination": map[string]any{path: "working_data"},
	}
	result, err := fixture.server.executeAgentSaveArtifacts(
		fixture.toolContext(t, "save-invalid-working-json", input), fixture.identity, "save-invalid-working-json", input,
	)
	if !errors.Is(err, errAgentSaveArtifactsNoResults) {
		t.Fatalf("invalid working JSON=%#v err=%v", result, err)
	}
	failures := agentSaveArtifactFailures(t, result)
	if len(failures) != 1 {
		t.Fatalf("invalid JSON failures=%#v", failures)
	}
	assertAgentSaveArtifactFailure(t, failures[0], path, "invalid_json_artifact")
	assertAgentSaveArtifactsNoCanonicalWrites(t, fixture)
}

func TestRunnerWorkingDataReportReferencePolicy(t *testing.T) {
	store := openRunnerArtifactCompletionStore(t)
	artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "retained-reference-source", ProjectID: "project-a", Name: "source.html", Kind: "text/html",
		Content: []byte(`<html><script>const path="reports/upstream.csv";</script></html>`), CreatedBy: "runner",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceStore: store}
	commits := []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifact.ID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}}
	for _, test := range []struct {
		mode string
		want []string
	}{
		{mode: "", want: []string{"missing_artifact_reference:upstream.csv in source.html"}},
		{mode: "working_data", want: []string{}},
		{mode: "snapshot", want: []string{"missing_artifact_reference:upstream.csv in source.html"}},
	} {
		t.Run("retention="+test.mode, func(t *testing.T) {
			if test.mode != "" {
				if err := store.SetArtifactRetentionMode(context.Background(), artifact.ID, artifact.ProjectID, "local", test.mode); err != nil {
					t.Fatal(err)
				}
			}
			failures, err := server.validateSessionRunnerCrossArtifactConsistency(context.Background(), artifact.ProjectID, commits)
			if err != nil || !reflect.DeepEqual(failures, test.want) {
				t.Fatalf("report-reference findings=%#v want=%#v err=%v", failures, test.want, err)
			}
		})
	}
	if err := store.SetArtifactRetentionMode(context.Background(), artifact.ID, artifact.ProjectID, "local", "working_data"); err != nil {
		t.Fatal(err)
	}
	failures, err := server.validateSessionRunnerCrossArtifactConsistency(context.Background(), artifact.ProjectID, commits, "See reports/upstream.csv.")
	if want := []string{"missing_artifact_reference:upstream.csv in final_response.md"}; err != nil || !reflect.DeepEqual(failures, want) {
		t.Fatalf("final response lost reference policy: findings=%#v err=%v", failures, err)
	}
}
