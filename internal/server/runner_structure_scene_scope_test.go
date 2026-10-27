package server

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func sceneScopeFixture(t *testing.T) (*agentSaveArtifactsFixture, structureSceneManifest, []byte) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	base := writeSceneTestArtifact(t, f.store, "base", f.stream.ProjectID, "base.pdb", "ATOM base", f.stream.FrameID)
	derived := writeSceneTestArtifact(t, f.store, "derived", f.stream.ProjectID, "derived.pdb", "ATOM derived", f.stream.FrameID)
	pixels := writeVisualReviewSizedPNG(t, filepath.Join(f.projectPath, "scene.png"), 240, 140)
	image := writeSceneTestArtifactBytes(t, f.store, "preview", f.stream.ProjectID, "scene.png", pixels, "image/png", f.stream.FrameID)
	return f, structureSceneManifest{
		Schema: structureSceneManifestSchema, SceneID: "comparison",
		MotherStructure:   structureSceneArtifactRef{Name: "base.pdb", VersionID: base.ID},
		DerivedStructures: []structureSceneArtifactRef{{Name: "derived.pdb", VersionID: derived.ID}},
		Layers: []structureSceneLayer{
			{VersionID: base.ID, Role: "mother", Representation: "cartoon", Visible: true},
			{VersionID: derived.ID, Role: "derived", Representation: "ball-and-stick", Visible: true},
		},
		PreviewImage: structureScenePreviewImage{Name: "scene.png", VersionID: image.ID, SHA256: image.ContentSHA256},
	}, pixels
}

func writeSceneScopeManifest(t *testing.T, f *agentSaveArtifactsFixture, id string, manifest structureSceneManifest) transcriptstore.ArtifactReferenceInput {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, version, err := f.store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: id, ProjectID: f.stream.ProjectID, Name: id + ".json", ContentType: "application/json",
		Content: bytes.NewReader(raw), CreatedBy: "runner", RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID, IsIntermediate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return transcriptstore.ArtifactReferenceInput{ArtifactID: id, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}
}

func TestStructureSceneSelectedManifestUsesOnlyPublishedRootContext(t *testing.T) {
	for _, mode := range []string{"published context", "cited manifest", "unselected draft", "foreign root", "selected immutable versions"} {
		t.Run(mode, func(t *testing.T) {
			f, manifest, pixels := sceneScopeFixture(t)
			var selected []transcriptstore.ArtifactReferenceInput
			switch mode {
			case "unselected draft":
				_, image, err := f.store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
					ArtifactID: "preview", ProjectID: f.stream.ProjectID, Name: "scene.png", ContentType: "image/png",
					Content: bytes.NewReader(pixels), CreatedBy: "runner", RootFrameID: f.stream.RootFrameID, FrameID: f.stream.FrameID, IsIntermediate: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				manifest.PreviewImage.VersionID, manifest.PreviewImage.SHA256 = image.ID, image.ContentSHA256
			case "foreign root":
				if _, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: "foreign-root", ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
					t.Fatal(err)
				}
				image := writeSceneTestArtifactBytes(t, f.store, "foreign-preview", f.stream.ProjectID, "foreign.png", pixels, "image/png", "foreign-root")
				manifest.PreviewImage = structureScenePreviewImage{Name: "foreign.png", VersionID: image.ID, SHA256: image.ContentSHA256}
			case "selected immutable versions":
				selected = append(selected,
					transcriptstore.ArtifactReferenceInput{ArtifactID: "base", VersionID: manifest.MotherStructure.VersionID, Relation: transcriptstore.ArtifactRelationProduced},
					transcriptstore.ArtifactReferenceInput{ArtifactID: "derived", VersionID: manifest.DerivedStructures[0].VersionID, Relation: transcriptstore.ArtifactRelationProduced})
				writeSceneTestArtifact(t, f.store, "base", f.stream.ProjectID, "base.pdb", "ATOM newer unrelated version", f.stream.FrameID)
			}
			current := writeSceneScopeManifest(t, f, "current-manifest", manifest)
			if mode == "cited manifest" {
				current.Relation = transcriptstore.ArtifactRelationCited
			}
			selected = append(selected, current)
			advisory, err := f.server.verifySessionRunnerVisualArtifactEvidence(sessionstore.Session{ID: f.stream.FrameID}, selected)
			if err != nil {
				t.Fatalf("context comparison returned an unexpected hard failure: %v", err)
			}
			if mode == "unselected draft" || mode == "foreign root" {
				if advisory == nil || advisory.Code != completionAdvisoryStructurePreview {
					t.Fatalf("unowned or unpublished context certified the scene: %#v", advisory)
				}
			} else if advisory != nil {
				t.Fatalf("exact selected manifest could not use published root context: %#v", advisory)
			}
			if intermediate, found, err := f.store.ArtifactVersionIntermediate(current.VersionID); err != nil || !found || !intermediate {
				t.Fatalf("validation published a candidate: intermediate=%t found=%t error=%v", intermediate, found, err)
			}
		})
	}
}

func TestStructureSceneHistoricalManifestCannotCertifyCurrentDelivery(t *testing.T) {
	f, oldScene, _ := sceneScopeFixture(t)
	old := writeSceneScopeManifest(t, f, "old-manifest", oldScene)
	if err := f.store.PublishArtifactVersion(context.Background(), old.VersionID, old.ArtifactID, f.stream.ProjectID, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	mother := writeSceneTestArtifact(t, f.store, "current-base", f.stream.ProjectID, "current-base.pdb", "ATOM current base", f.stream.FrameID)
	derived := writeSceneTestArtifact(t, f.store, "current-derived", f.stream.ProjectID, "current-derived.pdb", "ATOM current derived", f.stream.FrameID)
	current := []transcriptstore.ArtifactReferenceInput{
		{ArtifactID: "current-base", VersionID: mother.ID, Relation: transcriptstore.ArtifactRelationProduced},
		{ArtifactID: "current-derived", VersionID: derived.ID, Relation: transcriptstore.ArtifactRelationProduced},
	}
	for _, mode := range []string{"unselected historical manifest", "selected unrelated manifest"} {
		t.Run(mode, func(t *testing.T) {
			selected := append([]transcriptstore.ArtifactReferenceInput(nil), current...)
			if mode == "selected unrelated manifest" {
				selected = append(selected, old)
			}
			advisory, err := f.server.verifySessionRunnerVisualArtifactEvidence(sessionstore.Session{ID: f.stream.FrameID}, selected)
			if err != nil || advisory == nil || advisory.Code != completionAdvisoryStructurePreview {
				t.Fatalf("old scene certified an unrelated current comparison: advisory=%#v error=%v", advisory, err)
			}
			if mode == "selected unrelated manifest" && !strings.Contains(strings.Join(advisory.Findings, " "), "current produced structure") {
				t.Fatalf("finding did not identify the missing current versions: %#v", advisory)
			}
		})
	}
	newScene := oldScene
	newScene.MotherStructure = structureSceneArtifactRef{Name: "current-base.pdb", VersionID: mother.ID}
	newScene.DerivedStructures = []structureSceneArtifactRef{{Name: "current-derived.pdb", VersionID: derived.ID}}
	newScene.Layers = []structureSceneLayer{
		{VersionID: mother.ID, Role: "mother", Representation: "cartoon", Visible: true},
		{VersionID: derived.ID, Role: "derived", Representation: "ball-and-stick", Visible: true},
	}
	selected := append(current, writeSceneScopeManifest(t, f, "current-manifest", newScene))
	if advisory, err := f.server.verifySessionRunnerVisualArtifactEvidence(sessionstore.Session{ID: f.stream.FrameID}, selected); err != nil || advisory != nil {
		t.Fatalf("a real current manifest did not resolve the warning: advisory=%#v error=%v", advisory, err)
	}
}
