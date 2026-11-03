package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"os/exec"
	"strings"
	"testing"
	workspace "x-science/internal/persistence/workspace"
)

// Valid text/blank PDF pages; markers are emitted only by the real extractor,
// never supplied by this fixture. No image, model, scientific data or renderer.
func studyTextPagesFixture(texts []string) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	kids := []string{}
	for _, text := range texts {
		page := len(objects) + 1
		stream := page + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", stream))
		content := "q Q\n"
		if text != "" {
			content = "BT /F1 12 Tf 10 100 Td (" + text + ") Tj ET\n"
		}
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(texts))
	var result bytes.Buffer
	result.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, result.Len())
		fmt.Fprintf(&result, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := result.Len()
	fmt.Fprintf(&result, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&result, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&result, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return result.Bytes()
}
func studyPDFBodyFixture(t *testing.T, texts []string, abstract string) (*agentSaveArtifactsFixture, workspace.LiteratureStudySource, workspace.LiteratureStudyGenerate) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: uuid.NewString(), ProjectID: f.stream.ProjectID, Name: "synthetic-pages.pdf", Kind: "pdf", Content: studyTextPagesFixture(texts), CreatedBy: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := f.store.ImportLiteratureArtifact(t.Context(), f.stream.OwnerID, workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Synthetic PDF body evidence", PageCount: len(texts), Metadata: map[string]any{"abstract": abstract}})
	if err != nil {
		t.Fatal(err)
	}
	source, view, err := f.store.ReadLiteratureStudy(t.Context(), f.stream.OwnerID, created.Item.ID, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	return f, source, workspace.LiteratureStudyGenerate{OperationID: uuid.NewString(), Language: "en-US", ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision}
}
func TestLiteratureStudyPDFBodyCoverageFromOriginalExtractor(t *testing.T) {
	for _, tool := range []string{"pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("existing original PDF tool unavailable: " + tool)
		}
	}
	for _, c := range []struct {
		name        string
		texts       []string
		abstract    string
		coverage    string
		unavailable bool
	}{
		{"blank-pdf-only", []string{"", ""}, "", "", true},
		{"blank-abstract-fallback", []string{"", ""}, "A saved synthetic abstract", "abstract", false},
		{"mixed-body-and-blank", []string{"Actual synthetic body", ""}, "", "passages", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, source, input := studyPDFBodyFixture(t, c.texts, c.abstract)
			calls := 0
			result, err := f.server.generateLiteratureStudy(t.Context(), f.stream.OwnerID, source.ItemID, input,
				func(ctx context.Context, value workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
					return f.server.literatureStudyPDF(ctx, value, f.stream.OwnerID)
				},
				func(ctx context.Context, prompt string) (workspace.LiteratureStudySections, string, error) {
					calls++
					if !strings.Contains(prompt, `"coverage":"`+c.coverage+`"`) {
						t.Errorf("false body coverage in prompt: %s", prompt)
					}
					return workspace.LiteratureStudySections{}, "synthetic-not-a-model", nil
				})
			if c.unavailable {
				if !errors.Is(err, errLiteratureStudySourceUnavailable) || calls != 0 {
					t.Fatalf("page markers became paper text: calls=%d result=%+v err=%v", calls, result, err)
				}
				return
			}
			if err != nil || result.Snapshot == nil || result.Snapshot.Coverage != c.coverage || calls != 1 {
				t.Fatalf("wrong extraction coverage: %+v calls=%d err=%v", result, calls, err)
			}
		})
	}
}
