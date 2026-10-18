package server

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextPublicBootstrapUsesActualScope(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "reading-scope")
	response := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+frame.ID+"/reading-context", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("reading context bootstrap status=%d body=%s", response.Code, response.Body.String())
	}
	view := p3DecodeObject(t, response)
	if view["schema"] != "x-science.pdf-reading-context.v1" || view["revision"] != float64(0) {
		t.Fatalf("bootstrap is not a canonical empty context: %#v", view)
	}
	scope, ok := view["scope"].(map[string]any)
	if !ok || scope["frame_id"] != frame.ID || scope["frame_incarnation_id"] != frame.IncarnationID ||
		scope["root_frame_id"] != frame.ID || scope["root_frame_incarnation_id"] != frame.IncarnationID || scope["project_id"] != frame.ProjectID {
		t.Fatalf("bootstrap scope is guessed or incomplete: %#v", view["scope"])
	}
	bindings, ok := view["bindings"].([]any)
	if !ok || len(bindings) != 0 {
		t.Fatalf("empty context bindings=%#v", view["bindings"])
	}
}

func TestPDFReadingContextLinkKeepsSelectedImmutableVersionWithoutSending(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "reading-link")
	artifact, selected, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "reading-document", ProjectID: frame.ProjectID, Name: "reading.pdf", Kind: "pdf",
		Content: pdfReadingContextFixture(), CreatedBy: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, latest, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: artifact.ID, ProjectID: frame.ProjectID, Name: "reading.pdf", Kind: "pdf",
		Content: append(pdfReadingContextFixture(), []byte("\n% newer immutable version\n")...), CreatedBy: "fixture",
	})
	if err != nil || selected.ID == latest.ID {
		t.Fatalf("distinct immutable fixture versions: selected=%s latest=%s err=%v", selected.ID, latest.ID, err)
	}
	before, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"schema": "x-science.pdf-reading-link.v1", "operation_id": uuid.NewString(),
		"expected_frame_incarnation_id": frame.IncarnationID, "expected_root_frame_id": frame.ID,
		"expected_root_frame_incarnation_id": frame.IncarnationID, "expected_revision": 0,
		"sources": []map[string]any{{"artifact_id": artifact.ID, "version_id": selected.ID}},
	}
	response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations/"+frame.ID+"/reading-context/link", body, "")
	if response.Code != http.StatusOK {
		t.Fatalf("link selected version status=%d body=%s", response.Code, response.Body.String())
	}
	view := p3DecodeObject(t, response)
	bindings, ok := view["bindings"].([]any)
	if !ok || len(bindings) != 1 {
		t.Fatalf("link did not return authoritative single binding: %#v", view)
	}
	binding, ok := bindings[0].(map[string]any)
	if !ok || binding["artifact_id"] != artifact.ID || binding["version_id"] != selected.ID ||
		binding["checksum"] != selected.ContentSHA256 || binding["page_count"] != float64(2) || view["revision"] != float64(1) {
		t.Fatalf("selected version was replaced or PDF not inspected: %#v", view)
	}
	after, err := store.ListFrameEvents(frame.ID, 0, 100)
	if err != nil || len(after) != len(before) {
		t.Fatalf("link submitted a task/input: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

// Two real PDF pages with distinct page objects and a complete xref table.
// This fixture has no scientific content or generated delivery artifact.
func pdfReadingContextFixture() []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 5 0 R >> >> /Contents 6 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 5 0 R >> >> /Contents 7 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for _, page := range []string{"first page", "second page"} {
		content := "BT /F1 12 Tf 10 100 Td (" + page + ") Tj ET\n"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	var result bytes.Buffer
	result.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, result.Len())
		fmt.Fprintf(&result, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := result.Len()
	fmt.Fprintf(&result, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&result, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&result, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return result.Bytes()
}
