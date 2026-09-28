package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sessionstore "synon-go/internal/persistence/sessions"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestCheminfoSkillSceneExampleUsesNativeContract(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "skills", "synonbiomed", "cheminfo-render", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, example, found := strings.Cut(string(raw), "```json\n")
	if !found {
		t.Fatal("rendering skill omits the native scene contract example")
	}
	example, _, found = strings.Cut(example, "\n```")
	if !found {
		t.Fatal("unterminated scene contract example")
	}
	// The documentation must not fabricate a real image digest.
	example = strings.ReplaceAll(example, "REPLACE_WITH_ACTUAL_IMAGE_SHA256", strings.Repeat("3", 64))
	var manifest structureSceneManifest
	if err := json.Unmarshal([]byte(example), &manifest); err != nil {
		t.Fatal(err)
	}
	structures := []sessionReviewerArtifactEvidence{
		{Name: "receptor.pdb", VersionID: "receptor-version"},
		{Name: "complex.pdb", VersionID: "complex-version"},
	}
	images := []sessionReviewerArtifactEvidence{
		{Name: "scene.png", VersionID: "preview-version", ContentSHA256: strings.Repeat("3", 64)},
	}
	if failures := validateStructureSceneManifest(manifest, structures, images); len(failures) != 0 {
		t.Fatalf("documented scene cannot be consumed: %v", failures)
	}
}

func TestStructureSceneManifestRequiresVisibleMotherAndDerivedLayers(t *testing.T) {
	structures := []sessionReviewerArtifactEvidence{
		{ArtifactID: "mother-artifact", VersionID: "mother-version", Name: "mother.pdb", ContentSHA256: strings.Repeat("1", 64)},
		{ArtifactID: "derived-artifact", VersionID: "derived-version", Name: "derived.pdb", ContentSHA256: strings.Repeat("2", 64)},
	}
	images := []sessionReviewerArtifactEvidence{{
		ArtifactID: "preview-artifact", VersionID: "preview-version", Name: "scene.png", ContentSHA256: strings.Repeat("3", 64),
	}}
	manifest := structureSceneManifest{
		Schema:  structureSceneManifestSchema,
		SceneID: "scene-1",
		MotherStructure: structureSceneArtifactRef{
			Name: "mother.pdb", VersionID: "mother-version",
		},
		DerivedStructures: []structureSceneArtifactRef{{Name: "derived.pdb", VersionID: "derived-version"}},
		Layers: []structureSceneLayer{
			{VersionID: "mother-version", Role: "mother", Representation: "cartoon", Visible: true},
			{VersionID: "derived-version", Role: "derived", Representation: "ball-and-stick", Visible: true},
		},
		PreviewImage: structureScenePreviewImage{
			Name: "scene.png", VersionID: "preview-version", SHA256: strings.Repeat("3", 64),
		},
	}
	if failures := validateStructureSceneManifest(manifest, structures, images); len(failures) != 0 {
		t.Fatalf("valid scene manifest rejected: %v", failures)
	}
	manifest.Layers[1].Visible = false
	failures := validateStructureSceneManifest(manifest, structures, images)
	if len(failures) == 0 || !strings.Contains(strings.Join(failures, " "), "must be visible") {
		t.Fatalf("invisible derived layer was accepted: %v", failures)
	}
}

func TestStructureSceneCompletionGateRequiresManifestForMultipleSnapshotStructures(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-scene-missing", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "frame-scene-missing", ProjectID: "project-scene-missing", AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	writeSceneTestArtifact(t, store, "mother-artifact", "project-scene-missing", "mother.pdb", "ATOM mother", "frame-scene-missing")
	writeSceneTestArtifact(t, store, "derived-artifact", "project-scene-missing", "derived.pdb", "ATOM derived", "frame-scene-missing")
	srv := New(Options{FileRoot: root, Workspace: store})
	err = srv.verifySessionRunnerVisualArtifactEvidence(sessionForSceneTest("frame-scene-missing", root), nil)
	if err == nil {
		t.Fatal("multiple snapshot structures passed without a structure-scene manifest")
	}
	var required *sessionRunnerVisualArtifactValidationRequired
	if !errors.As(err, &required) || len(required.StructureSceneFailures) == 0 ||
		!strings.Contains(strings.Join(required.StructureSceneFailures, " "), structureSceneManifestSchema) {
		t.Fatalf("unexpected scene contract error: %#v", err)
	}
	detail := required.runnerCorrection().Detail
	if strings.Contains(detail, "VisualReview") ||
		!strings.Contains(detail, "mother_structure") || !strings.Contains(detail, "derived_structures") {
		t.Fatalf("scene correction requests a retired tool or omits the native contract: %s", detail)
	}
}

func TestStructureSceneCompletionGateBindsPreviewBeforeIndependentReview(t *testing.T) {
	root := t.TempDir()
	store, err := workspace.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-scene-valid", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "frame-scene-valid", ProjectID: "project-scene-valid", AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	mother := writeSceneTestArtifact(t, store, "mother-artifact", "project-scene-valid", "mother.pdb", "ATOM mother", "frame-scene-valid")
	derived := writeSceneTestArtifact(t, store, "derived-artifact", "project-scene-valid", "derived.pdb", "ATOM derived", "frame-scene-valid")
	imageBytes := writeVisualReviewSizedPNG(t, filepath.Join(root, "scene.png"), 240, 140)
	imageDigest := sha256.Sum256(imageBytes)
	imageHash := hex.EncodeToString(imageDigest[:])
	imageArtifact := writeSceneTestArtifactBytes(t, store, "preview-artifact", "project-scene-valid", "scene.png", imageBytes, "image/png", "frame-scene-valid")
	manifestBytes, err := json.Marshal(structureSceneManifest{
		Schema:            structureSceneManifestSchema,
		SceneID:           "scene-1",
		MotherStructure:   structureSceneArtifactRef{Name: "mother.pdb", VersionID: mother.ID},
		DerivedStructures: []structureSceneArtifactRef{{Name: "derived.pdb", VersionID: derived.ID}},
		Layers: []structureSceneLayer{
			{VersionID: mother.ID, Role: "mother", Representation: "cartoon", Visible: true},
			{VersionID: derived.ID, Role: "derived", Representation: "ball-and-stick", Visible: true},
		},
		PreviewImage: structureScenePreviewImage{Name: "scene.png", VersionID: imageArtifact.ID, SHA256: imageHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSceneTestArtifactBytes(t, store, "scene-manifest-artifact", "project-scene-valid", "scene.json", manifestBytes, "application/json", "frame-scene-valid")
	// Retain a failed earlier attempt. It must not shadow the valid immutable
	// replacement merely because its filename sorts before the new manifest.
	writeSceneTestArtifactBytes(t, store, "old-manifest", "project-scene-valid", "0-old-scene.json",
		[]byte(`{"schema":"synon.structure-scene.v1","preview_image":"old.png"}`), "application/json", "frame-scene-valid")
	srv := New(Options{FileRoot: root, Workspace: store})
	disabled := sessionForSceneTest("frame-scene-valid", root)
	if err := srv.verifySessionRunnerVisualArtifactEvidence(disabled, nil); err != nil {
		t.Fatalf("disabled independent verifier rejected a structurally valid bound scene: %v", err)
	}
	enabled := disabled
	enabled.Orchestration = map[string]any{"sessionConfig": map[string]any{"verifier_mode": "on"}}
	if err := srv.verifySessionRunnerVisualArtifactEvidence(enabled, nil); err != nil {
		t.Fatalf("valid bound scene must reach the independent reviewer: %v", err)
	}
	// Save uses staged versions: completion must validate the selected draft
	// before publication, otherwise both phases permanently wait for each other.
	_, draft, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "scene-manifest-artifact", ProjectID: "project-scene-valid", Name: "scene.json", ContentType: "application/json",
		Content: bytes.NewReader(manifestBytes), CreatedBy: "runner", RootFrameID: "frame-scene-valid", FrameID: "frame-scene-valid", IsIntermediate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := transcriptstore.ArtifactReferenceInput{ArtifactID: "scene-manifest-artifact", VersionID: draft.ID, Relation: transcriptstore.ArtifactRelationProduced}
	for _, session := range []sessionstore.Session{disabled, enabled} {
		if err := srv.verifySessionRunnerVisualArtifactEvidence(session, []transcriptstore.ArtifactReferenceInput{selected}); err != nil {
			t.Fatalf("selected draft cannot reach publication: %v", err)
		}
	}
	if intermediate, found, err := store.ArtifactVersionIntermediate(draft.ID); err != nil || !found || !intermediate {
		t.Fatalf("validation published the draft early: intermediate=%v found=%v err=%v", intermediate, found, err)
	}
	for name, candidate := range map[string]transcriptstore.ArtifactReferenceInput{
		"unselected":     {},
		"wrong version":  {ArtifactID: selected.ArtifactID, VersionID: "unselected-version", Relation: selected.Relation},
		"wrong artifact": {ArtifactID: "another-artifact", VersionID: selected.VersionID, Relation: selected.Relation},
		"cited only":     {ArtifactID: selected.ArtifactID, VersionID: selected.VersionID, Relation: transcriptstore.ArtifactRelationCited},
	} {
		t.Run(name, func(t *testing.T) {
			if err := srv.verifySessionRunnerVisualArtifactEvidence(disabled, []transcriptstore.ArtifactReferenceInput{candidate}); err == nil {
				t.Fatal("an unselected draft satisfied the completion gate")
			}
		})
	}
	_, invalid, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: selected.ArtifactID, ProjectID: "project-scene-valid", Name: "scene.json", ContentType: "application/json",
		Content:   strings.NewReader(strings.Replace(string(manifestBytes), imageHash, strings.Repeat("0", 64), 1)),
		CreatedBy: "runner", RootFrameID: "frame-scene-valid", FrameID: "frame-scene-valid", IsIntermediate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected.VersionID = invalid.ID
	if err := srv.verifySessionRunnerVisualArtifactEvidence(disabled, []transcriptstore.ArtifactReferenceInput{selected}); err == nil {
		t.Fatal("selected draft bypassed preview digest validation")
	}
}

func sessionForSceneTest(frameID, root string) sessionstore.Session {
	return sessionstore.Session{ID: frameID, WorkDir: root, Orchestration: map[string]any{
		"sessionConfig": map[string]any{"verifier_mode": "off"},
	}}
}

func writeSceneTestArtifact(t *testing.T, store *workspace.Store, artifactID, projectID, name, content, frameID string) workspace.ArtifactVersion {
	return writeSceneTestArtifactBytes(t, store, artifactID, projectID, name, []byte(content), "text/plain", frameID)
}

func writeSceneTestArtifactBytes(t *testing.T, store *workspace.Store, artifactID, projectID, name string, content []byte, contentType, frameID string) workspace.ArtifactVersion {
	t.Helper()
	_, version, err := store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: artifactID, ProjectID: projectID, Name: name, ContentType: contentType,
		Content: bytes.NewReader(content), CreatedBy: "runner", RootFrameID: frameID, FrameID: frameID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}
