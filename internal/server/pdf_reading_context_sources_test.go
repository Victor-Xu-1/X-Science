package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextCandidatesAreBoundedAndSinglePageRemainsAttachable(t *testing.T) {
	f, current, original := newPDFReadingFixture(t)
	old := []byte("/Kids [3 0 R 4 0 R] /Count 2")
	replacement := []byte("/Kids [3 0 R] /Count 1")
	replacement = append(replacement, bytes.Repeat([]byte(" "), len(old)-len(replacement))...)
	single := bytes.Replace(pdfReadingContextFixture(), old, replacement, 1)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "single-reading-page", ProjectID: f.stream.ProjectID,
		Name: "single.pdf", Kind: "pdf", Content: single, CreatedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	singleSource := transcriptstore.PDFReadingSource{ArtifactID: artifact.ID, VersionID: version.ID}
	sources := []transcriptstore.PDFReadingSource{original.Sources[0], original.Sources[0], singleSource}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/candidates", pdfReadingCandidatesBody{
		Schema: "x-science.pdf-reading-candidates.v1", Sources: sources}, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("candidates HTTP %d %s", response.Code, response.Body.String())
	}
	candidates := p3DecodeObject(t, response)["candidates"].([]any)
	if len(candidates) != 2 || candidates[0].(map[string]any)["eligible"] != true ||
		candidates[1].(map[string]any)["eligible"] != false || candidates[1].(map[string]any)["reason"] != "single_page" {
		t.Fatalf("candidates=%#v", candidates)
	}
	tooMany := make([]transcriptstore.PDFReadingSource, 101)
	for index := range tooMany {
		tooMany[index] = singleSource
	}
	rejected := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/candidates", pdfReadingCandidatesBody{
		Schema: "x-science.pdf-reading-candidates.v1", Sources: tooMany}, f.stream.OwnerID)
	if rejected.Code != 400 {
		t.Fatalf("unbounded candidate request=%d", rejected.Code)
	}
	link := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current),
		Sources: []transcriptstore.PDFReadingSource{singleSource}}
	if rejected = p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", link, f.stream.OwnerID); rejected.Code != 409 {
		t.Fatalf("single-page link=%d", rejected.Code)
	}
	// Ordinary artifact attachment remains valid; no reading binding is inferred.
	input := webConversationSendInput{Content: "Inspect the ordinary single-page attachment.", LoadingID: uuid.NewString(),
		ArtifactRefs: []webConversationArtifactReferenceInput{{ArtifactID: artifact.ID, VersionID: version.ID}}}
	accepted := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", input, f.stream.OwnerID)
	if accepted.Code != 202 {
		t.Fatalf("single-page attachment was blocked: %d %s", accepted.Code, accepted.Body.String())
	}
	message := pdfReadingUserMessageForTest(t, f, webString(p3DecodeObject(t, accepted)["msg_id"]))
	if mapValue(message["inputData"])[transcriptstore.PDFReadingSnapshotKey] != nil {
		t.Fatal("ordinary attachment acquired reading binding")
	}
}

func TestPDFReadingContextThreeDocumentLimitAndExactMutationBody(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	sources := []transcriptstore.PDFReadingSource{}
	for _, id := range []string{"second", "third", "fourth"} {
		artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: id, ProjectID: f.stream.ProjectID,
			Name: id + ".pdf", Kind: "pdf", Content: pdfReadingContextFixture(), CreatedBy: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, transcriptstore.PDFReadingSource{ArtifactID: artifact.ID, VersionID: version.ID})
	}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current), Sources: sources[:2]}
	accepted := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if accepted.Code != 200 {
		t.Fatalf("three total HTTP %d %s", accepted.Code, accepted.Body.String())
	}
	var next transcriptstore.PDFReadingContext
	if err := json.Unmarshal(accepted.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	changed := body
	changed.Sources = sources[:1]
	if response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", changed, f.stream.OwnerID); response.Code != 409 {
		t.Fatalf("changed subset reused UUID: %d", response.Code)
	}
	fourth := body
	fourth.OperationID = uuid.NewString()
	fourth.PDFReadingExpected = readingExpectedForTest(next)
	fourth.Sources = sources[2:]
	if response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", fourth, f.stream.OwnerID); response.Code != 409 {
		t.Fatalf("fourth PDF was accepted: %d", response.Code)
	}
	actual, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || len(actual.Bindings) != 3 || actual.Revision != next.Revision {
		t.Fatalf("limit failure partially changed state: %#v %v", actual, err)
	}
}

func TestPDFReadingContextManagedReaderUsesSelectedVersionNotCurrentHead(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	changed := bytes.Replace(pdfReadingContextFixture(), []byte("first page"), []byte("WRONG HEAD"), 1)
	if _, _, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: current.Bindings[0].ArtifactID, ProjectID: f.stream.ProjectID,
		Name: "reading.pdf", Kind: "pdf", Content: changed, CreatedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	result, err := f.server.executeAgentWorkspaceFileTool(withAgentWorkspaceReadBudget(context.Background(), 16000), f.identity, "read_file",
		map[string]any{"version_id": current.Bindings[0].VersionID, "offset": 1, "limit": 200})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := unwrapAgentRuntimeRichToolResponse(result)
	content := stringValue(mapValue(value)["content"])
	if !strings.Contains(content, "first page") || strings.Contains(content, "WRONG HEAD") {
		t.Fatalf("managed reader changed version: %#v", value)
	}
}
