package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/tools/securefetch"
)

func downloadedContinuationSource(t *testing.T) (*agentSaveArtifactsFixture, *sessionRunnerChatRun, map[string]any, string) {
	t.Helper()
	fixture := newAgentSaveArtifactsFixture(t)
	const source = "https://source.example.org/material.html"
	const body = `<!doctype html><html><body><script>const template="{{ client.value }}";const file="reports/upstream.csv";</script>Raw source bytes.</body></html>`
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	transport := upstream.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true // Controlled TLS fixture with a public virtual hostname only.
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	fixture.server.publicScientificFiles = securefetch.New(securefetch.Options{
		HTTPClient: &http.Client{Transport: transport}, TestOnlyAllowCustomTransport: true, Resolver: scientificRedirectTestResolver{},
	})
	fixture.server.publicScientificDownloadSlots = make(chan struct{}, 2)
	sourceID := appendAgentPublicScientificSourceCheckpoint(t, fixture, "raw-material-source", source, false, nil)
	input := map[string]any{"url": source, "source_tool_call_id": sourceID, "human_description": "Retaining original source data"}
	result, err := fixture.server.executeAgentPublicScientificFileDownload(
		agentPublicScientificToolContext(t, fixture, "download-material", input), fixture.identity, "download-material", input,
	)
	if err != nil || result["ok"] != true {
		t.Fatalf("controlled native source download failed: %#v %v", result, err)
	}
	artifacts := agentSaveArtifactResults(t, result)
	if len(artifacts) != 1 {
		t.Fatalf("download artifacts=%#v", artifacts)
	}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: fixture.claim}}
	run.ContinuationArtifactReferences = artifactReferencesFromRunnerEntries([]eventjournal.Entry{{Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolName": "download_public_scientific_file", "toolPhase": "completed", "toolResult": result,
	}}})
	return fixture, run, artifacts[0], body
}

func TestRunnerContinuationPreservesCanonicalSourceRole(t *testing.T) {
	fixture, run, source, body := downloadedContinuationSource(t)
	commits, err := fixture.server.sessionRunnerArtifactCommitReferences(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	want := []transcriptstore.ArtifactReferenceInput{{ArtifactID: stringValue(source["artifact_id"]),
		VersionID: stringValue(source["version_id"]), Relation: transcriptstore.ArtifactRelationConsumed}}
	if !reflect.DeepEqual(commits, want) {
		t.Errorf("replayed payload promoted an immutable consumed source: got=%#v want=%#v", commits, want)
	}
	failures, err := fixture.server.validateSessionRunnerCrossArtifactConsistency(t.Context(), fixture.stream.ProjectID, commits)
	if err != nil || len(failures) != 0 {
		t.Errorf("raw source was treated as an unfinished report: failures=%#v err=%v", failures, err)
	}
	if mode, found, err := fixture.store.ArtifactRetentionMode(want[0].ArtifactID); err != nil || !found || mode != "snapshot" {
		t.Fatalf("fix rewrote canonical retention: mode=%q found=%t err=%v", mode, found, err)
	}
	_, version, reader, found, err := fixture.store.OpenArtifactVersionContent(want[0].VersionID)
	if err != nil || !found {
		t.Fatalf("immutable source missing: found=%t err=%v", found, err)
	}
	defer reader.Close()
	bytes, err := io.ReadAll(reader)
	digest := sha256.Sum256([]byte(body))
	if err != nil || string(bytes) != body || version.ContentSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("immutable raw source changed: err=%v checksum=%s", err, version.ContentSHA256)
	}
	workspaceBytes, err := os.ReadFile(filepath.Join(fixture.projectPath, "material.html"))
	if err != nil || string(workspaceBytes) != body {
		t.Fatalf("original workspace source changed: %v", err)
	}
}

func TestRunnerContinuationCanonicalRoleUsesExactVersion(t *testing.T) {
	fixture, run, source, _ := downloadedContinuationSource(t)
	artifactID := stringValue(source["artifact_id"])
	_, later, err := fixture.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: artifactID, ProjectID: fixture.stream.ProjectID, Name: "material.html", Kind: "text/html",
		Content: []byte("<html>New legacy report {{ unfinished }}</html>"), CreatedBy: "legacy-save",
	})
	if err != nil {
		t.Fatal(err)
	}
	run.ContinuationArtifactReferences = []transcriptstore.ArtifactReferenceInput{{
		ArtifactID: artifactID, VersionID: later.ID, Relation: transcriptstore.ArtifactRelationProduced,
	}}
	commits, err := fixture.server.sessionRunnerArtifactCommitReferences(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	produced := false
	for _, ref := range commits {
		produced = produced || ref.ArtifactID == artifactID && ref.VersionID == later.ID && ref.Relation == transcriptstore.ArtifactRelationProduced
	}
	if !produced {
		t.Fatalf("another version borrowed the old source role: %#v", commits)
	}
	failures, err := fixture.server.validateSessionRunnerCrossArtifactConsistency(t.Context(), fixture.stream.ProjectID, commits)
	if err != nil || len(failures) == 0 || !strings.Contains(strings.Join(failures, ","), "unresolved_template_marker:") {
		t.Fatalf("genuine report lost strict template policy: findings=%#v err=%v", failures, err)
	}
}
