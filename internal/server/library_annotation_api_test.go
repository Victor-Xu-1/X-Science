package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestLibraryAnnotationSelectionSurvivesOriginAndReadsThroughAdmittedTool(t *testing.T) {
	f, reading, _ := newPDFReadingFixture(t)
	if _, err := f.store.CreateProject(workspace.CreateProjectInput{ID: "library-origin-project", UserID: f.stream.OwnerID, Name: "Independent origin", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	origin, originVersion, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "library-origin-artifact", ProjectID: "library-origin-project", Name: "paper.pdf", Kind: "pdf", Content: pdfReadingContextFixture()})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: origin.ID, VersionID: originVersion.ID, Title: "Independent PDF"}, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("import HTTP%d %s", response.Code, response.Body.String())
	}
	var imported workspace.LiteratureReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/literature/attachment-versions/" + imported.VersionID + "/annotations"
	version := imported.Item.Attachments[0].Versions[0]
	response = p3JSONRequest(t, f.server, http.MethodPost, endpoint, map[string]any{"type": "text_selection", "text": "Saved note 🧪", "selection_text": "Synthetic words", "page_number": 2, "content_checksum": version.Checksum}, f.stream.OwnerID)
	if response.Code != http.StatusCreated {
		t.Fatalf("save HTTP%d %s", response.Code, response.Body.String())
	}
	saved := p3DecodeObject(t, response)
	if saved["target_key"] != "litv:"+imported.VersionID {
		t.Fatal("Library write was attached to an origin artifact")
	}
	if err := f.store.DeleteProject(origin.ProjectID); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		response = p3JSONRequest(t, f.server, http.MethodGet, endpoint, nil, f.stream.OwnerID)
		page := p3DecodeObject(t, response)
		if response.Code != http.StatusOK || page["total"] != float64(1) || page["annotations"].([]any)[0].(map[string]any)["id"] != saved["id"] {
			t.Fatalf("Library note lost after origin deletion: HTTP%d", response.Code)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		foreign := p3JSONRequest(t, f.server, method, endpoint, map[string]any{"text": "foreign"}, "other-owner")
		if foreign.Code == http.StatusOK || foreign.Code == http.StatusCreated {
			t.Fatal("foreign owner accessed Library notes")
		}
	}
	reading = unlinkPDFReadingForTest(t, f, reading)
	response = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(reading), Sources: []transcriptstore.PDFReadingSource{{SourceKind: "literature-attachment-version", ItemID: imported.Item.ID, AttachmentID: imported.AttachmentID, VersionID: imported.VersionID}}}, f.stream.OwnerID)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reading) != nil {
		t.Fatalf("Library link HTTP%d", response.Code)
	}
	gateway := admitPDFAnnotationGatewayForTest(t, f, readingSelectionForTest(reading))
	gateway.kernel = nil
	value, err := gateway.executeAgentToolResponseUncached(context.Background(), agentruntime.ToolCall{ID: "library-notes"}, "list_pdf_annotations", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	result := pdfAnnotationResultForTest(t, value)
	if result["total"] != float64(1) || result["annotations"].([]any)[0].(map[string]any)["id"] != saved["id"] {
		t.Fatal("native reader did not read the same canonical Library note")
	}
	trash := true
	receipt, err := f.store.PatchLiteratureItem(t.Context(), f.stream.OwnerID, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: imported.Item.MetadataRevision, Deleted: &trash})
	if err != nil {
		t.Fatal(err)
	}
	if hidden := p3JSONRequest(t, f.server, http.MethodGet, endpoint, nil, f.stream.OwnerID); hidden.Code == http.StatusOK {
		t.Fatal("Trash leaked a live preview")
	}
	value, err = gateway.executeAgentToolResponseUncached(t.Context(), agentruntime.ToolCall{ID: "accepted-tombstone"}, "list_pdf_annotations", map[string]any{})
	if err != nil || pdfAnnotationResultForTest(t, value)["total"] != float64(1) {
		t.Fatalf("accepted read lost retained notes: %v", err)
	}
	trash = false
	if _, err = f.store.PatchLiteratureItem(t.Context(), f.stream.OwnerID, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: receipt.Item.MetadataRevision, Deleted: &trash}); err != nil {
		t.Fatal(err)
	}
	if restored := p3JSONRequest(t, f.server, http.MethodGet, endpoint, nil, f.stream.OwnerID); restored.Code != http.StatusOK {
		t.Fatal("Restore did not recover the same Library notes")
	}
}
