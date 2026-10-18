package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextFinalizedUploadUsesExistingImmutableAuthority(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	content := pdfReadingContextFixture()
	checksum := sha256.Sum256(content)
	upload, err := f.store.InitAttachmentUpload(context.Background(), workspace.InitAttachmentUploadInput{
		UserID: f.stream.OwnerID, ProjectID: f.stream.ProjectID, Filename: "uploaded-reading.pdf", ContentType: "application/pdf",
		TotalSize: int64(len(content)), ChunkSize: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.SaveAttachmentChunk(context.Background(), f.stream.OwnerID, upload.ID, 0, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	artifact, version, _, err := f.store.FinalizeArtifactUpload(workspace.WithMutationIdempotencyKey(context.Background(), "reading-upload-finalize"),
		f.stream.OwnerID, upload.ID, fmt.Sprintf("%x", checksum))
	if err != nil {
		t.Fatal(err)
	}
	body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current),
		Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}}}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("finalized upload link HTTP %d %s", response.Code, response.Body.String())
	}
	var view transcriptstore.PDFReadingContext
	if err = json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Bindings) != 2 || view.Bindings[1].VersionID != version.ID || view.Bindings[1].Checksum != version.ContentSHA256 {
		t.Fatalf("finalized upload gained a different version authority: %#v", view)
	}
}

func TestPDFReadingContextCloneKeepsHistoricalEvidenceWithoutLiveBinding(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	request := webConversationSendInput{Content: "Use this frozen page as context.", LoadingID: uuid.NewString(), ReadingContext: readingSelectionForTest(current)}
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/conversations/"+f.stream.FrameID+"/messages", request, f.stream.OwnerID)
	if response.Code != 202 {
		t.Fatalf("input HTTP %d %s", response.Code, response.Body.String())
	}
	claimSkillControlInput(t, f)
	finishSkillControlInput(t, f)
	cloneInput := workspace.CloneConversationInput{OwnerUserID: f.stream.OwnerID, SourceFrameID: f.stream.FrameID,
		ExpectedSourceIncarnationID: current.Scope.FrameIncarnationID, TargetFrameID: "reading-clone", TargetName: "Reading history copy"}
	clone, err := f.store.CloneConversationWithTranscript(context.Background(), cloneInput)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, clone.Frame.ID)
	if err != nil || empty.Revision != 0 || len(empty.Bindings) != 0 {
		t.Fatalf("clone inherited control owner: %#v %v", empty, err)
	}
	events, err := f.repo.ListProjectedEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: clone.Transcript.TargetStream.UID, OwnerID: f.stream.OwnerID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	historical := false
	for _, event := range events {
		var payload map[string]any
		if json.Unmarshal(event.ResolvedPayloadJSON, &payload) != nil {
			t.Fatal("invalid cloned payload")
		}
		if value := mapValue(payload["inputData"])[transcriptstore.PDFReadingSnapshotKey]; value != nil {
			snapshot, err := transcriptstore.DecodePDFReadingSnapshot(value)
			if err != nil || len(snapshot.Bindings) != 1 || snapshot.Bindings[0].VersionID != current.Bindings[0].VersionID {
				t.Fatalf("lost immutable history: %#v %v", snapshot, err)
			}
			historical = true
		}
	}
	if !historical {
		t.Fatal("clone lost historical reading evidence")
	}
	unlinkPDFReadingForTest(t, f, current)
	if _, err = f.store.CloneConversationWithTranscript(context.Background(), cloneInput); err != nil {
		t.Fatalf("source binding changed clone retry: %v", err)
	}
}

func TestPDFReadingContextOversizedAndFalsePDFNeverBecomeBindings(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	for _, sourceCase := range []struct {
		id      string
		content []byte
		reason  string
	}{
		{"false-pdf", []byte("This filename is not a PDF."), "not_pdf"},
		{"oversized-pdf", append([]byte("%PDF-1.4\n"), make([]byte, int(transcriptstore.MaxPDFReadingBytes))...), "too_large"},
	} {
		artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: sourceCase.id, ProjectID: f.stream.ProjectID,
			Name: sourceCase.id + ".pdf", Kind: "pdf", Content: sourceCase.content, CreatedBy: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		sources := []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}}
		response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/candidates", pdfReadingCandidatesBody{
			Schema: "x-science.pdf-reading-candidates.v1", Sources: sources}, f.stream.OwnerID)
		if response.Code != 200 {
			t.Fatalf("candidate HTTP %d %s", response.Code, response.Body.String())
		}
		items := p3DecodeObject(t, response)["candidates"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["reason"] != sourceCase.reason {
			t.Fatalf("candidate=%#v", items)
		}
		body := pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current), Sources: sources}
		if rejected := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID); rejected.Code != 409 {
			t.Fatalf("invalid binding=%d", rejected.Code)
		}
	}
	after, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || after.Revision != current.Revision || len(after.Bindings) != 1 {
		t.Fatalf("invalid source changed context: %#v %v", after, err)
	}
}
