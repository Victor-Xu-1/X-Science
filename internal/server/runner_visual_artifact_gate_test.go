package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"

	sessionstore "synon-go/internal/persistence/sessions"
	workspace "synon-go/internal/persistence/workspace"
)

func TestVisualArtifactCorrectionPreservesActionableFailureWithManyArtifacts(t *testing.T) {
	err := &sessionRunnerVisualArtifactValidationRequired{
		Artifacts:              []string{"a.pdb", "b.pdb", "c.pdb", "d.pdb", "e.pdb", "f.pdb", "g.pdb", "h.pdb", "i.pdb"},
		StructureSceneFailures: []string{"visible derived layer is missing for version-z"},
	}
	for _, detail := range []string{err.Error(), err.runnerCorrection().Detail} {
		if !strings.Contains(detail, err.StructureSceneFailures[0]) {
			t.Fatalf("artifact name truncation hid the actionable repair condition: %s", detail)
		}
	}
	if len(err.runnerCorrection().Condition.Visual.UnboundNames) != 10 {
		t.Fatal("display summarization must not truncate the complete recovery condition")
	}
}

func TestVisualArtifactCompletionGateSkipsSessionsWithoutWorkspaceFrame(t *testing.T) {
	srv := New(Options{FileRoot: t.TempDir()})
	if err := srv.verifySessionRunnerVisualArtifactEvidence(sessionstore.Session{ID: "im:wechat:standalone"}, nil); err != nil {
		t.Fatalf("non-workspace session unexpectedly required workspace visual evidence: %v", err)
	}
}

func TestVisualArtifactCompletionGateHonorsDisabledVerifierMode(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-visual-policy", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "frame-visual-policy", ProjectID: "project-visual-policy", AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	imageBytes := writeVisualReviewSizedPNG(t, filepath.Join(root, "ranking.png"), 240, 140)
	if _, _, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "artifact-ranking-policy", ProjectID: "project-visual-policy", Name: "ranking.png",
		ContentType: "image/png", Content: bytes.NewReader(imageBytes), CreatedBy: "runner",
		RootFrameID: "frame-visual-policy", FrameID: "frame-visual-policy",
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{FileRoot: root, Workspace: store})
	disabled := sessionstore.Session{ID: "frame-visual-policy", WorkDir: root, Orchestration: map[string]any{
		"sessionConfig": map[string]any{"verifier_mode": "off"},
	}}
	if err := srv.verifySessionRunnerVisualArtifactEvidence(disabled, nil); err != nil {
		t.Fatalf("disabled verifier unexpectedly required visual evidence: %v", err)
	}
	enabled := disabled
	enabled.Orchestration = map[string]any{"sessionConfig": map[string]any{"verifier_mode": "on"}}
	if err := srv.verifySessionRunnerVisualArtifactEvidence(enabled, nil); err != nil {
		t.Fatalf("pre-review integrity gate prevented the enabled independent reviewer from running: %v", err)
	}
}

func TestTerminalReviewerRequiresImageBytesNotMetadata(t *testing.T) {
	scope := &sessionReviewerEvidenceScope{requireVisualReads: true, artifacts: map[string]sessionReviewerArtifactEvidence{
		"image-v1": {Name: "figure.png", VersionID: "image-v1"},
	}}
	for _, receipts := range [][]sessionReviewerEvidenceReceipt{
		nil,
		{{VersionID: "image-v1", Complete: true}},
		{{VersionID: "other", Complete: true, visual: true}},
		{{VersionID: "image-v1", visual: true}},
	} {
		if scope.validateVisualReads(receipts) == nil {
			t.Fatal("missing or metadata-only image evidence passed")
		}
	}
	if err := scope.validateVisualReads([]sessionReviewerEvidenceReceipt{{VersionID: "image-v1", Complete: true, visual: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalReviewerAcceptsCanonicalImageReadReceipt(t *testing.T) {
	data := writeVisualReviewSizedPNG(t, filepath.Join(t.TempDir(), "scene.png"), 240, 140)
	hash := sha256.Sum256(data)
	artifact := sessionReviewerArtifactEvidence{ArtifactID: "image", Name: "scene.png", VersionID: "image-v1", ContentSHA256: hex.EncodeToString(hash[:])}
	binding := map[string]any{"stream_uid": "stream", "runner_attempt": 1, "review_index": 0,
		"artifact_inventory_sha256": strings.Repeat("a", 64), "review_scope": "logical_task_terminal"}
	scope, err := newSessionReviewerEvidenceScope(binding, sessionReviewerWorkspaceEvidence{Artifacts: []sessionReviewerArtifactEvidence{artifact}})
	if err != nil {
		t.Fatal(err)
	}
	if !scope.requireVisualReads {
		t.Fatal("terminal review did not require image evidence")
	}
	raw, err := readAgentWorkspaceFile(context.Background(), bytes.NewReader(data), "scene.png", "image/png", int64(len(data)), nil)
	if err != nil {
		t.Fatal(err)
	}
	rich := raw.(agentRuntimeRichToolResponse)
	value, err := scope.record("read_file", "read-image", map[string]any{"version_id": "image-v1"}, rich.value, rich.parts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	messages := []agentruntime.Message{
		{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "read-image", Name: "read_file", Arguments: json.RawMessage(`{"version_id":"image-v1"}`)}}},
		{Role: "tool", ToolCallID: "read-image", Content: string(encoded), Parts: rich.parts},
	}
	if receipts, err := validateSessionReviewerEvidence(sessionRunnerReview{Verdict: "pass"}, messages, scope); err != nil || len(receipts) != 1 {
		t.Fatalf("canonical image read was rejected: receipts=%v err=%v", receipts, err)
	}
	bad := append([]byte(nil), data...)
	bad[len(bad)-1] ^= 1
	rich.parts[0].Media.Source.Data = bad
	if _, err := scope.record("read_file", "tampered", map[string]any{"version_id": "image-v1"}, rich.value, rich.parts); err == nil {
		t.Fatal("tampered visual read was accepted")
	}
}

func TestVisualArtifactCompletionGateRejectsChangedImageBytes(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	data := writeVisualReviewSizedPNG(t, filepath.Join(root, "figure.png"), 240, 140)
	_, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "figure", ProjectID: "project", Name: "figure.png", ContentType: "image/png", Content: bytes.NewReader(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := sessionReviewerArtifactEvidence{ArtifactID: "figure", VersionID: version.ID, Name: "figure.png", ContentSHA256: version.ContentSHA256}
	srv := New(Options{FileRoot: root, Workspace: store})
	if err := srv.verifyVisualArtifactEvidence([]sessionReviewerArtifactEvidence{artifact}); err != nil {
		t.Fatal(err)
	}
	artifact.ContentSHA256 = strings.Repeat("0", 64)
	var invalid *sessionRunnerVisualArtifactValidationRequired
	if err := srv.verifyVisualArtifactEvidence([]sessionReviewerArtifactEvidence{artifact}); !errors.As(err, &invalid) {
		t.Fatalf("changed bytes accepted: %v", err)
	}
}
