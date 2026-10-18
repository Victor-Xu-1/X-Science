package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func newPDFReadingFixture(t *testing.T) (*agentSaveArtifactsFixture, transcriptstore.PDFReadingContext, pdfReadingMutationBody) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	enabled := true
	if _, err := f.store.CreateAgent(workspace.CreateAgentInput{ID: "reading-assistant", UserID: f.stream.OwnerID, Name: "OPERON",
		DisplayName: "Reading assistant", Description: "Reads explicitly selected document versions.", Unrestricted: true, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	finishSkillControlInput(t, f)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "reading-document", ProjectID: f.stream.ProjectID,
		Name: "reading.pdf", Kind: "pdf", Content: pdfReadingContextFixture(), CreatedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(),
		PDFReadingExpected: readingExpectedForTest(current), Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}}}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("real link HTTP %d: %s", response.Code, response.Body.String())
	}
	if err = json.Unmarshal(response.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	return f, current, body
}

func readingPathForTest(f *agentSaveArtifactsFixture) string {
	return "/api/conversations/" + f.stream.FrameID + "/reading-context"
}

func readingExpectedForTest(current transcriptstore.PDFReadingContext) transcriptstore.PDFReadingExpected {
	revision := current.Revision
	return transcriptstore.PDFReadingExpected{FrameIncarnationID: current.Scope.FrameIncarnationID, RootFrameID: current.Scope.RootFrameID,
		RootFrameIncarnationID: current.Scope.RootFrameIncarnationID, Revision: &revision}
}

func readingSelectionForTest(current transcriptstore.PDFReadingContext) *transcriptstore.PDFReadingSelection {
	ids := []string{}
	for _, binding := range current.Bindings {
		ids = append(ids, binding.BindingID)
	}
	selection := &transcriptstore.PDFReadingSelection{Schema: transcriptstore.PDFReadingSelectionSchema, PDFReadingExpected: readingExpectedForTest(current), BindingIDs: ids}
	if len(ids) > 0 {
		selection.ActiveBindingID = ids[0]
		selection.PageNumber = 2
	}
	return selection
}

func unlinkPDFReadingForTest(t *testing.T, f *agentSaveArtifactsFixture, current transcriptstore.PDFReadingContext) transcriptstore.PDFReadingContext {
	t.Helper()
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-unlink.v1", OperationID: uuid.NewString(),
		PDFReadingExpected: readingExpectedForTest(current), BindingID: current.Bindings[0].BindingID}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/unlink", body, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("unlink HTTP %d: %s", response.Code, response.Body.String())
	}
	var next transcriptstore.PDFReadingContext
	if err := json.Unmarshal(response.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	return next
}
