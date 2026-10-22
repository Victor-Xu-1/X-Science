package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureLibraryCrossProjectAcceptedPDFSurvivesSourceDeletion(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	current = unlinkPDFReadingForTest(t, f, current)
	if _, err := f.store.CreateProject(workspace.CreateProjectInput{ID: "literature-source-project", UserID: f.stream.OwnerID, Name: "Source project"}); err != nil {
		t.Fatal(err)
	}
	pdf := pdfReadingContextFixture()
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "library-source", ProjectID: "literature-source-project", Name: "paper.pdf", Kind: "application/pdf", Content: pdf})
	if err != nil {
		t.Fatal(err)
	}
	input := workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Real Library paper", ProjectID: "literature-source-project"}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", input, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("import HTTP%d %s", response.Code, response.Body.String())
	}
	var receipt workspace.LiteratureReceipt
	if err = json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: artifact.ID, ProjectID: artifact.ProjectID, Name: "paper.pdf", Kind: "application/pdf", Content: append(append([]byte(nil), pdf...), []byte("\n% changed head\n")...)}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteProject("literature-source-project"); err != nil {
		t.Fatal(err)
	}
	retry := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", input, f.stream.OwnerID)
	if retry.Code != 200 || p3DecodeObject(t, retry)["version_id"] != receipt.VersionID {
		t.Fatalf("lost ACK after delete=%d %s", retry.Code, retry.Body.String())
	}
	list := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/items", nil, f.stream.OwnerID)
	if list.Code != 200 || len(p3DecodeObject(t, list)["items"].([]any)) != 1 {
		t.Fatalf("independent catalog HTTP%d %s", list.Code, list.Body.String())
	}
	content := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/attachment-versions/"+receipt.VersionID+"/content", nil, f.stream.OwnerID)
	if content.Code != 200 || !bytes.Equal(content.Body.Bytes(), pdf) {
		t.Fatalf("independent content HTTP%d", content.Code)
	}
	source := transcriptstore.PDFReadingSource{SourceKind: "literature-attachment-version", ItemID: receipt.Item.ID, AttachmentID: receipt.AttachmentID, VersionID: receipt.VersionID}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current), Sources: []transcriptstore.PDFReadingSource{source}}
	response = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("cross-project Library link HTTP%d %s", response.Code, response.Body.String())
	}
	if err = json.Unmarshal(response.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	request := webConversationSendInput{Content: "Read the selected Library PDF page.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(current)}
	accepted := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if accepted.Code != 202 {
		t.Fatalf("Library send HTTP%d %s", accepted.Code, accepted.Body.String())
	}
	message := pdfReadingUserMessageForTest(t, f, webString(p3DecodeObject(t, accepted)["msg_id"]))
	if refs, _ := decodeUserArtifactReferences(message["artifactRefs"]); len(refs) != 0 {
		t.Fatal("Library invented artifactRefs")
	}
	text := runnerModelMessageText(message)
	if !strings.Contains(text, receipt.VersionID) || !strings.Contains(text, `"page_number":2`) {
		t.Fatalf("Library-only runner lost frozen PDF: %s", text)
	}
	readers := inputAttachmentReaderStatesFromRunnerEntries([]eventjournal.Entry{{Message: message}})
	if len(readers) != 1 || readers[0].Read {
		t.Fatalf("Library reader state=%#v", readers)
	}
	access, found, err := f.store.GetKernelFrameAccess(f.stream.FrameID)
	if err != nil || !found {
		t.Fatal(err)
	}
	identity := &agentKernelContext{access: access, workspaceDir: t.TempDir()}
	readable, err := f.server.openAcceptedLibraryReadingVersion(context.Background(), identity, f.stream.OwnerID, receipt.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(readable.reader)
	_ = readable.reader.Close()
	if err != nil || !bytes.Equal(actual, pdf) {
		t.Fatal("accepted source bytes changed")
	}
	result, err := f.server.executeAgentWorkspaceReadFile(context.Background(), identity, f.stream.OwnerID, f.stream.ProjectID, map[string]any{"version_id": receipt.VersionID, "pages": []int{2}})
	if err != nil || result == nil {
		t.Fatalf("real builtin PDF read err=%v result=%#v", err, result)
	}
	value, _ := unwrapAgentRuntimeRichToolResponse(result)
	readView, ok := value.(map[string]any)
	if !ok || readView["source_version_id"] != receipt.VersionID || readView["source_sha256"] != version.ContentSHA256 || readView["code"] != nil {
		t.Fatalf("builtin read did not prove exact Library PDF: %#v", value)
	}
	deleted := true
	patch := workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}
	if response = p3JSONRequest(t, f.server, http.MethodPatch, "/api/literature/items/"+receipt.Item.ID, patch, f.stream.OwnerID); response.Code != 200 {
		t.Fatalf("tombstone HTTP%d", response.Code)
	}
	readable, err = f.server.openAcceptedLibraryReadingVersion(context.Background(), identity, f.stream.OwnerID, receipt.VersionID)
	if err != nil {
		t.Fatal("accepted version lost on item tombstone", err)
	}
	_ = readable.reader.Close()
}

func TestLiteratureLibraryRejectsForeignOwnerMixedSourceAndGuessedReads(t *testing.T) {
	f, current, original := newPDFReadingFixture(t)
	input := workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: original.Sources[0].ArtifactID, VersionID: original.Sources[0].VersionID, Title: "Paper"}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", input, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("import=%d %s", response.Code, response.Body.String())
	}
	var receipt workspace.LiteratureReceipt
	_ = json.Unmarshal(response.Body.Bytes(), &receipt)
	if foreign := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/attachment-versions/"+receipt.VersionID+"/content", nil, "foreign-owner"); foreign.Code != 404 {
		t.Fatalf("foreign content=%d", foreign.Code)
	}
	mixed := transcriptstore.PDFReadingSource{SourceKind: "literature-attachment-version", ArtifactID: original.Sources[0].ArtifactID, ItemID: receipt.Item.ID, AttachmentID: receipt.AttachmentID, VersionID: receipt.VersionID}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current), Sources: []transcriptstore.PDFReadingSource{mixed}}
	if denied := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID); denied.Code != 400 {
		t.Fatalf("mixed source accepted=%d", denied.Code)
	}
	access, _, _ := f.store.GetKernelFrameAccess(f.stream.FrameID)
	if _, err := f.server.openAcceptedLibraryReadingVersion(context.Background(), &agentKernelContext{access: access}, f.stream.OwnerID, receipt.VersionID); err == nil {
		t.Fatal("unaccepted Library version became task read authority")
	}
}

func TestLiteratureRetainedPreviewRequiresAcceptedFrame(t *testing.T) {
	f, current, original := newPDFReadingFixture(t)
	input := workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: original.Sources[0].ArtifactID, VersionID: original.Sources[0].VersionID, Title: "Retained paper"}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", input, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("import HTTP%d %s", response.Code, response.Body.String())
	}
	var receipt workspace.LiteratureReceipt
	_ = json.Unmarshal(response.Body.Bytes(), &receipt)
	livePreview := p3JSONRequest(t, f.server, http.MethodGet, "/api/literature/attachment-versions/"+receipt.VersionID+"/content?frame_id="+f.stream.FrameID, nil, f.stream.OwnerID)
	if livePreview.Code != 200 || !bytes.Equal(livePreview.Body.Bytes(), pdfReadingContextFixture()) {
		t.Fatalf("pre-send live preview HTTP%d %s", livePreview.Code, livePreview.Body.String())
	}
	source := transcriptstore.PDFReadingSource{SourceKind: "literature-attachment-version", ItemID: receipt.Item.ID, AttachmentID: receipt.AttachmentID, VersionID: receipt.VersionID}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current), Sources: []transcriptstore.PDFReadingSource{source}}
	response = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("link HTTP%d %s", response.Code, response.Body.String())
	}
	_ = json.Unmarshal(response.Body.Bytes(), &current)
	request := webConversationSendInput{Content: "Read this Library paper.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(current)}
	if accepted := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID); accepted.Code != 202 {
		t.Fatalf("accept HTTP%d %s", accepted.Code, accepted.Body.String())
	}
	other := uuid.NewString()
	if _, err := f.store.CreateFrame(workspace.CreateFrameInput{ID: other, ProjectID: f.stream.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	otherContext, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, other)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.server.inspectPDFReadingSource(context.Background(), f.stream.OwnerID, f.stream.ProjectID, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.LinkPDFReadingContext(context.Background(), workspace.PDFReadingMutation{OwnerID: f.stream.OwnerID, FrameID: other, OperationID: uuid.NewString(), Expected: readingExpectedForTest(otherContext), Sources: []transcriptstore.PDFReadingSource{source}, Bindings: []transcriptstore.PDFReadingBinding{binding}}); err != nil {
		t.Fatal(err)
	}
	deleted := true
	if _, err = f.store.PatchLiteratureItem(context.Background(), f.stream.OwnerID, receipt.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	path := "/api/literature/attachment-versions/" + receipt.VersionID + "/content"
	if denied := p3JSONRequest(t, f.server, http.MethodGet, path, nil, f.stream.OwnerID); denied.Code != 404 {
		t.Fatalf("ordinary tombstone read=%d", denied.Code)
	}
	preview := p3JSONRequest(t, f.server, http.MethodGet, path+"?frame_id="+f.stream.FrameID, nil, f.stream.OwnerID)
	if preview.Code != 200 || !bytes.Equal(preview.Body.Bytes(), pdfReadingContextFixture()) {
		t.Fatalf("accepted retained preview HTTP%d %s", preview.Code, preview.Body.String())
	}
	if denied := p3JSONRequest(t, f.server, http.MethodGet, path+"?frame_id="+other, nil, f.stream.OwnerID); denied.Code != 404 {
		t.Fatalf("mutable link became accepted proof=%d", denied.Code)
	}
	if denied := p3JSONRequest(t, f.server, http.MethodGet, path+"?frame_id="+f.stream.FrameID, nil, "foreign-owner"); denied.Code != 404 {
		t.Fatalf("foreign accepted frame read=%d", denied.Code)
	}
}
