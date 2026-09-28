package server

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	sessionstore "synon-go/internal/persistence/sessions"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestVisualDeliveryUsesOnlyExactSelectedImages(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	valid := writeVisualReviewSizedPNG(t, filepath.Join(t.TempDir(), "valid.png"), 240, 140)
	save := func(id string, raw []byte, draft bool) transcriptstore.ArtifactReferenceInput {
		t.Helper()
		_, version, err := f.store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
			ArtifactID: id, ProjectID: "project-save", Name: id + ".png", ContentType: "image/png",
			Content: bytes.NewReader(raw), CreatedBy: "runner", RootFrameID: "frame-save", FrameID: "frame-save", IsIntermediate: draft,
		})
		if err != nil {
			t.Fatal(err)
		}
		return transcriptstore.ArtifactReferenceInput{ArtifactID: id, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}
	}
	selected := save("valid", valid, true)
	bad := save("unfinished", []byte("incomplete render"), true)
	session := sessionstore.Session{ID: "frame-save", WorkDir: f.projectPath}
	if err := f.server.verifySessionRunnerVisualArtifactEvidence(session, []transcriptstore.ArtifactReferenceInput{selected}); err != nil {
		t.Fatalf("unselected intermediate image blocked valid delivery: %v", err)
	}
	if intermediate, found, err := f.store.ArtifactVersionIntermediate(selected.VersionID); err != nil || !found || !intermediate {
		t.Fatal("validation published the selected draft")
	}
	if err := f.server.verifySessionRunnerVisualArtifactEvidence(session, []transcriptstore.ArtifactReferenceInput{bad}); err == nil {
		t.Fatal("selected corrupt image passed")
	}
	bad.Relation = transcriptstore.ArtifactRelationCited
	if err := f.server.verifySessionRunnerVisualArtifactEvidence(session, []transcriptstore.ArtifactReferenceInput{selected, bad}); err != nil {
		t.Fatalf("cited input was treated as a generated delivery: %v", err)
	}
	// A newer workspace version must not replace the selected immutable bytes.
	save("valid", []byte("new unfinished render"), true)
	if err := f.server.verifySessionRunnerVisualArtifactEvidence(session, []transcriptstore.ArtifactReferenceInput{selected}); err != nil {
		t.Fatalf("new current version replaced the selected version: %v", err)
	}
	wrong := selected
	wrong.ArtifactID = "different-artifact"
	if _, err := f.server.sessionRunnerVisualDeliveryEvidence("project-save", []transcriptstore.ArtifactReferenceInput{wrong}); err == nil {
		t.Fatal("mismatched artifact/version pair passed")
	}
	if _, err := f.server.sessionRunnerVisualDeliveryEvidence("another-project", []transcriptstore.ArtifactReferenceInput{selected}); err == nil {
		t.Fatal("foreign project received selected image evidence")
	}
}

func TestVisualDeliveryRejectsHeaderOnlyPNG(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	_, version, err := f.store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: "corrupt", ProjectID: "project-save", Name: "corrupt.png", ContentType: "image/png",
		Content: bytes.NewReader([]byte{137, 80, 78, 71, 13, 10, 26, 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.verifyVisualArtifactEvidence([]sessionReviewerArtifactEvidence{{ArtifactID: "corrupt", VersionID: version.ID, Name: "corrupt.png", ContentSHA256: version.ContentSHA256}}); err == nil {
		t.Fatal("signature and matching hash without image data passed")
	}
}
