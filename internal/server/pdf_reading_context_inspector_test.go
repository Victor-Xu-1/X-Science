package server

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPDFReadingContextMissingInspectorIsNotAStorageFailure(t *testing.T) {
	f, linked, body := newPDFReadingFixture(t)
	unlinked := unlinkPDFReadingForTest(t, f, linked)
	// A mutation response includes transient Changed state and omits the
	// private replay proof. Compare durable state to durable state in full.
	before, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || before.Revision != unlinked.Revision || len(before.Bindings) != 0 {
		t.Fatalf("durable unlink baseline: %#v err=%v", before, err)
	}
	body.OperationID = uuid.NewString()
	body.PDFReadingExpected = readingExpectedForTest(before)
	statusBefore, _, _, _ := f.server.artifactSystemDoctorArea()
	// Exercise the real missing-host-dependency branch, not a fake PDF parser.
	// The fixture above was inspected by the actual installed binary.
	t.Setenv("PATH", t.TempDir())
	statusAfter, _, evidence, next := f.server.artifactSystemDoctorArea()
	if statusAfter != statusBefore || evidence["hasPDFReadingInspector"] != false || evidence["pdfReadingInspectorScope"] != "backend-host-PATH" || !strings.Contains(strings.Join(next, " "), "poppler-utils") {
		t.Errorf("missing inspector was hidden by doctor: evidence=%#v next=%#v", evidence, next)
	}
	candidates := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/candidates", pdfReadingCandidatesBody{
		Schema: "x-science.pdf-reading-candidates.v1", Sources: body.Sources}, f.stream.OwnerID)
	if candidates.Code != http.StatusOK {
		t.Errorf("missing inspector was reported as storage: HTTP %d %s", candidates.Code, candidates.Body.String())
	} else {
		items := p3DecodeObject(t, candidates)["candidates"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["eligible"] != false || items[0].(map[string]any)["reason"] != "inspection_unavailable" {
			t.Errorf("missing inspector candidate=%#v", items)
		}
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if response.Code != http.StatusConflict || p3DecodeObject(t, response)["code"] != "PDF_READING_SOURCE_UNAVAILABLE" {
		t.Errorf("missing inspector link: HTTP %d %s", response.Code, response.Body.String())
	}
	after, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("unavailable inspector mutated reading state: before=%#v after=%#v err=%v", before, after, err)
	}
	ordinary := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", webConversationSendInput{
		Content: "Continue the ordinary conversation without PDF reading.", LoadingID: uuid.NewString()}, f.stream.OwnerID)
	if ordinary.Code != http.StatusAccepted {
		t.Fatalf("missing PDF dependency blocked ordinary input: HTTP %d %s", ordinary.Code, ordinary.Body.String())
	}
}
