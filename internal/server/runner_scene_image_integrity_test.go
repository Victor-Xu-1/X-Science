package server

import (
	"encoding/json"
	"errors"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestOptionalSceneStillRejectsCorruptBoundImageOutsideNewSelection(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	base := writeSceneTestArtifact(t, f.store, "base", f.stream.ProjectID, "base.pdb", "ATOM base", f.stream.SessionID)
	derived := writeSceneTestArtifact(t, f.store, "derived", f.stream.ProjectID, "derived.pdb", "ATOM derived", f.stream.SessionID)
	image := writeSceneTestArtifactBytes(t, f.store, "preview", f.stream.ProjectID, "scene.png", []byte("not an image"), "image/png", f.stream.SessionID)
	manifest, err := json.Marshal(structureSceneManifest{
		Schema: structureSceneManifestSchema, SceneID: "comparison",
		MotherStructure:   structureSceneArtifactRef{Name: "base.pdb", VersionID: base.ID},
		DerivedStructures: []structureSceneArtifactRef{{Name: "derived.pdb", VersionID: derived.ID}},
		Layers: []structureSceneLayer{
			{VersionID: base.ID, Role: "mother", Representation: "cartoon", Visible: true},
			{VersionID: derived.ID, Role: "derived", Representation: "ball-and-stick", Visible: true},
		},
		PreviewImage: structureScenePreviewImage{Name: "scene.png", VersionID: image.ID, SHA256: image.ContentSHA256},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestVersion := writeSceneTestArtifactBytes(t, f.store, "manifest", f.stream.ProjectID, "scene.json", manifest, "application/json", f.stream.SessionID)
	advisory, err := f.server.verifySessionRunnerVisualArtifactEvidence(sessionstore.Session{ID: f.stream.SessionID}, []transcriptstore.ArtifactReferenceInput{
		{ArtifactID: "manifest", VersionID: manifestVersion.ID, Relation: transcriptstore.ArtifactRelationProduced},
	})
	var integrity *sessionRunnerVisualArtifactValidationRequired
	if advisory != nil || !errors.As(err, &integrity) || len(integrity.Targets) != 1 ||
		integrity.Targets[0].VersionID != image.ID || integrity.Targets[0].Failure != "invalid_image_integrity" {
		t.Fatalf("optional scene hid corrupt exact preview: advisory=%#v error=%v", advisory, err)
	}
	if got := sessionRunnerVisualCorrectionIsAdvisory(recoveredRunnerCorrection{
		ReasonCode: integrity.runnerCorrection().ReasonCode, Condition: integrity.runnerCorrection().Condition,
	}); got {
		t.Fatal("image failure was demoted during recovery")
	}
}
