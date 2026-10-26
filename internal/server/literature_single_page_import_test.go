package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Reuse the exact one-page derivation from the existing Reading regression,
// preserving its complete xref offsets. These are synthetic fixture bytes,
// not a user document or generated scientific delivery.
func librarySinglePageFixture(t *testing.T) []byte {
	t.Helper()
	original := []byte("/Kids [3 0 R 4 0 R] /Count 2")
	replacement := []byte("/Kids [3 0 R] /Count 1")
	replacement = append(replacement, bytes.Repeat([]byte(" "), len(original)-len(replacement))...)
	raw := pdfReadingContextFixture()
	if bytes.Count(raw, original) != 1 {
		t.Fatal("SETUP_FAILURE: known complete-xref fixture changed")
	}
	return bytes.Replace(raw, original, replacement, 1)
}

func librarySinglePageRequest(t *testing.T, ctx context.Context, app http.Handler, method, target, owner string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := newLoopbackTestRequest(method, target, bytes.NewReader(encoded)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", owner)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func TestLiteratureImportSinglePageUsesOwnedImmutableAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	raw := librarySinglePageFixture(t)
	path, cleanup, err := stageAgentWorkspacePDFSource(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("SETUP_FAILURE: stage known synthetic PDF: %v", err)
	}
	defer cleanup()
	inspected, err := inspectStagedPDFDocument(ctx, path)
	if err != nil || inspected.Encrypted || inspected.PageCount != 1 {
		t.Fatalf("SETUP_FAILURE: actual installed PDF inspector did not validate the existing one-page fixture: facts=%+v err=%v", inspected, err)
	}
	digest := sha256.Sum256(raw)
	t.Logf("ONE_PAGE_FIXTURE_CONFIRMED: actual parser pages=1 encrypted=false bytes=%d sha256=%x", len(raw), digest)
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	const owner = "local"
	const project = "library-single-page-project"
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: project, UserID: owner, Name: "Synthetic owned PDF project"}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "single-page-source", ProjectID: project, Name: "single.pdf", Kind: "application/pdf", Content: raw, CreatedBy: owner,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	server := New(Options{Workspace: store, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		closed, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		_ = server.Close(closed)
		_ = store.Close()
	})
	app := server.Handler()
	t.Run("reading_retains_single_page_exclusion", func(t *testing.T) {
		_, err := server.inspectPDFReadingSource(ctx, owner, project, transcriptstore.PDFReadingSource{ArtifactID: artifact.ID, VersionID: version.ID})
		var candidate *pdfReadingCandidateError
		if !errors.As(err, &candidate) || candidate.reason != "single_page" {
			t.Fatalf("Reading single_page policy changed: %v", err)
		}
	})
	// A newer current head must not replace the selected immutable source.
	if _, newer, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: artifact.ID, ProjectID: project, Name: artifact.Name, Kind: "application/pdf", Content: append(append([]byte(nil), raw...), []byte("\n% later immutable head\n")...), CreatedBy: owner,
	}); err != nil || newer.ID == version.ID {
		t.Fatalf("SETUP_FAILURE: create distinct immutable head: %v", err)
	}
	input := workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Owned single-page reference", ProjectID: project}
	response := librarySinglePageRequest(t, ctx, app, http.MethodPost, "/api/literature/import-artifact-version", owner, input)
	if response.Code != http.StatusOK {
		t.Fatalf("SINGLE_PAGE_IMPORT_RED: valid inspected one-page PDF must import via the original HTTP owner, got%d body=%s", response.Code, response.Body.String())
	}
	var receipt workspace.LiteratureReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.OperationID != input.OperationID || receipt.Item.ID == "" || receipt.AttachmentID == "" || receipt.VersionID == "" || len(receipt.Item.Attachments) != 1 || len(receipt.Item.Attachments[0].Versions) != 1 {
		t.Fatalf("incomplete original import receipt: %+v", receipt)
	}
	imported := receipt.Item.Attachments[0].Versions[0]
	if imported.ID != receipt.VersionID || imported.AttachmentID != receipt.AttachmentID || imported.PageCount != 1 || !imported.Available || imported.Checksum != version.ContentSHA256 || imported.SizeBytes != int64(len(raw)) {
		t.Fatalf("import changed selected immutable facts: %+v", imported)
	}
	contentPath := "/api/literature/attachment-versions/" + receipt.VersionID + "/content"
	content := librarySinglePageRequest(t, ctx, app, http.MethodGet, contentPath, owner, nil)
	if content.Code != 200 || !bytes.Equal(content.Body.Bytes(), raw) {
		t.Fatalf("owned immutable content mismatch: HTTP%d", content.Code)
	}
	if foreign := librarySinglePageRequest(t, ctx, app, http.MethodGet, contentPath, "foreign-owner", nil); foreign.Code != 404 {
		t.Fatalf("foreign immutable content exposed: HTTP%d", foreign.Code)
	}
	foreignInput := input
	foreignInput.OperationID = uuid.NewString()
	if foreign := librarySinglePageRequest(t, ctx, app, http.MethodPost, "/api/literature/import-artifact-version", "foreign-owner", foreignInput); foreign.Code != 404 {
		t.Fatalf("foreign source owner accepted: HTTP%d", foreign.Code)
	}
	// Preserve the existing receipt-first replay even after source deletion.
	if err := store.DeleteProject(project); err != nil {
		t.Fatal(err)
	}
	retry := librarySinglePageRequest(t, ctx, app, http.MethodPost, "/api/literature/import-artifact-version", owner, input)
	var replay workspace.LiteratureReceipt
	if retry.Code != 200 || json.Unmarshal(retry.Body.Bytes(), &replay) != nil || replay.Item.ID != receipt.Item.ID || replay.AttachmentID != receipt.AttachmentID || replay.VersionID != receipt.VersionID {
		t.Fatalf("same-operation receipt replay after source deletion failed: HTTP%d", retry.Code)
	}
	list := librarySinglePageRequest(t, ctx, app, http.MethodGet, "/api/literature/items", owner, nil)
	var page workspace.LiteraturePage
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != receipt.Item.ID {
		t.Fatalf("catalogue was duplicated or lost: HTTP%d", list.Code)
	}
	content = librarySinglePageRequest(t, ctx, app, http.MethodGet, contentPath, owner, nil)
	if content.Code != 200 || !bytes.Equal(content.Body.Bytes(), raw) {
		t.Fatalf("independent Library version lost after source deletion: HTTP%d", content.Code)
	}
}
