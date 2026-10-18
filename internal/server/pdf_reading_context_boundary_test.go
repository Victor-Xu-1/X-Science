package server

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestPDFReadingContextSourceScopeAndChecksumAreNotFilenameAuthority(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	for _, sourceCase := range []struct{ project, owner string }{{"foreign-reading-project", "another-owner"}, {"same-owner-other-project", f.stream.OwnerID}} {
		project := createP3Project(t, f.store, sourceCase.project, sourceCase.owner)
		artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: sourceCase.project + "-pdf",
			ProjectID: project.ID, Name: "reading.pdf", Kind: "pdf", Content: pdfReadingContextFixture(), CreatedBy: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", pdfReadingMutationBody{
			Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(), PDFReadingExpected: readingExpectedForTest(current),
			Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}},
		}, f.stream.OwnerID)
		if response.Code != 409 {
			t.Fatalf("cross-scope source HTTP %d %s", response.Code, response.Body.String())
		}
	}
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "tampered-reading-pdf", ProjectID: f.stream.ProjectID,
		Name: "reading.pdf", Kind: "pdf", Content: pdfReadingContextFixture(), CreatedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt only this fresh fixture's inline bytes without changing its stored
	// immutable checksum. No application metadata or admission guards are bypassed.
	changed := bytes.Replace(pdfReadingContextFixture(), []byte("first page"), []byte("wrong page"), 1)
	if version.StoragePath != "" {
		t.Fatal("checksum fixture unexpectedly uses external storage")
	}
	if err = f.store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE artifact_versions SET content=? WHERE id=?", changed, version.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/candidates", pdfReadingCandidatesBody{
		Schema: "x-science.pdf-reading-candidates.v1", Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}},
	}, f.stream.OwnerID)
	if response.Code != 200 {
		t.Fatalf("inspection HTTP %d %s", response.Code, response.Body.String())
	}
	candidates := p3DecodeObject(t, response)["candidates"].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["reason"] != "integrity_unavailable" {
		t.Fatalf("checksum was not checked: %#v", candidates)
	}
}

func TestPDFReadingContextConcurrentMutationUsesOneRevisionCAS(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	bodies := []pdfReadingMutationBody{}
	for _, id := range []string{"concurrent-reading-a", "concurrent-reading-b"} {
		artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: id, ProjectID: f.stream.ProjectID,
			Name: id + ".pdf", Kind: "pdf", Content: pdfReadingContextFixture(), CreatedBy: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, pdfReadingMutationBody{Schema: "x-science.pdf-reading-link.v1", OperationID: uuid.NewString(),
			PDFReadingExpected: readingExpectedForTest(current), Sources: []transcriptstore.PDFReadingSource{{ArtifactID: artifact.ID, VersionID: version.ID}}})
	}
	start := make(chan struct{})
	statuses := make(chan int, len(bodies))
	var group sync.WaitGroup
	for _, body := range bodies {
		group.Add(1)
		go func(body pdfReadingMutationBody) {
			defer group.Done()
			<-start
			response := p3JSONRequest(t, f.server, http.MethodPost, readingPathForTest(f)+"/link", body, f.stream.OwnerID)
			statuses <- response.Code
		}(body)
	}
	close(start)
	group.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("two clients did not use final CAS: %#v", counts)
	}
	latest, err := f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID)
	if err != nil || latest.Revision != current.Revision+1 || len(latest.Bindings) != 2 {
		t.Fatalf("partial/lost mutation: %#v %v", latest, err)
	}
}

func TestPDFReadingContextMalformedReservedStateIsVisibleAndPreserved(t *testing.T) {
	f, current, _ := newPDFReadingFixture(t)
	err := f.store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE frame_runtime_metadata SET context_data=json_set(context_data,'$._pdf_reading_context','opaque') WHERE frame_id=?", f.stream.FrameID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.server, http.MethodGet, readingPathForTest(f), nil, f.stream.OwnerID)
	if response.Code != 503 || p3DecodeObject(t, response)["code"] != "PDF_READING_STATE_UNAVAILABLE" {
		t.Fatalf("bad state was presented as empty: %d %s", response.Code, response.Body.String())
	}
	metadata, found, err := f.store.GetFrameRuntimeMetadata(f.stream.FrameID)
	if err != nil || !found {
		t.Fatal(err)
	}
	metadata.ContextData = map[string]any{"unrelated": "still writable"}
	if _, err = f.store.SetFrameRuntimeMetadata(f.stream.FrameID, metadata); err != nil {
		t.Fatal(err)
	}
	stored, _, err := f.store.GetFrameRuntimeMetadata(f.stream.FrameID)
	if err != nil || stored.ContextData[transcriptstore.PDFReadingMetadataKey] != "opaque" {
		t.Fatalf("generic writer erased diagnostic: %#v %v", stored.ContextData, err)
	}
	if _, err = f.store.GetPDFReadingContext(context.Background(), f.stream.OwnerID, f.stream.FrameID); err == nil {
		t.Fatalf("invalid revision %d silently reset", current.Revision)
	}
}
