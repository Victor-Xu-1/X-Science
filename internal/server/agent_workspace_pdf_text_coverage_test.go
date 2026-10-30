package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestPDFSelectedTextCoverageUsesNativeBody(t *testing.T) {
	requirePDFRangeTools(t)
	for _, test := range []struct {
		name, body string
		pages      []int
		textPages  []int
		emptyPages []int
	}{
		{name: "empty pages keep visuals", pages: []int{1, 2}, emptyPages: []int{1, 2}},
		{name: "mixed pages retain order and text", body: "Actual selected page body", pages: []int{2, 1}, textPages: []int{1}, emptyPages: []int{2}},
		{name: "body resembling a marker is real text", body: "[PDF page 999]", pages: []int{1, 2}, textPages: []int{1}, emptyPages: []int{2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := selectedPDFTextCoverageFixture(test.body, "")
			original := append([]byte(nil), raw...)
			ctx := withAgentWorkspaceReadBudget(context.Background(), 4096)
			result, err := readAgentWorkspaceFile(ctx, bytes.NewReader(raw), "source.pdf", "application/pdf", int64(len(raw)), map[string]any{
				"file_path": "source.pdf", "pages": test.pages,
			})
			if err != nil {
				t.Fatal(err)
			}
			value, parts := unwrapAgentRuntimeRichToolResponse(result)
			view := mapValue(value)
			extraction := mapValue(view["text_extraction"])
			wantStatus, wantReader := "parsed", "parsed_and_attached"
			if len(test.textPages) == 0 {
				wantStatus, wantReader = "no_extractable_text", "attached"
			}
			if extraction["status"] != wantStatus || mapValue(view["reader_contract"])["status"] != wantReader {
				t.Errorf("host page markers established false body evidence: extraction=%v reader=%v", extraction, view["reader_contract"])
			}
			if got := agentWorkspacePageValues(extraction["text_pages"]); !equalPDFCoveragePages(got, test.textPages) {
				t.Errorf("native text pages=%v want=%v", got, test.textPages)
			}
			if got := agentWorkspacePageValues(extraction["no_text_pages"]); !equalPDFCoveragePages(got, test.emptyPages) {
				t.Errorf("native empty pages=%v want=%v", got, test.emptyPages)
			}
			if len(parts) != len(test.pages) || numberValue(view["visual_parts"]) != int64(len(test.pages)) {
				t.Fatalf("selected visual pages were lost: parts=%d view=%v", len(parts), view)
			}
			for index, part := range parts {
				if part.Type != agentruntime.ContentPartImage || part.Media == nil || len(part.Media.Source.Data) == 0 || part.Media.Filename != fmt.Sprintf("source-page-%d.png", test.pages[index]) {
					t.Errorf("selected visual identity changed: index=%d part=%+v", index, part)
				}
			}
			content := stringValue(view["content"])
			if !strings.Contains(content, "[PDF page 1]") || !strings.Contains(content, "[PDF page 2]") || test.body != "" && !strings.Contains(content, test.body) {
				t.Errorf("page provenance or actual body was removed: %q", content)
			}
			if test.pages[0] == 2 && strings.Index(content, "[PDF page 2]") > strings.Index(content, "[PDF page 1]") {
				t.Errorf("selected-page stream order changed: %q", content)
			}
			readWith := mapValue(view["text_read_with"])
			if !agentWorkspaceReadResultFits(ctx, view) || view["next_offset"] != nil || readWith["pages"] != nil || readWith["offset"] != 1 || readWith["file_path"] != "source.pdf" {
				t.Errorf("coverage broke bounded selected-page continuation: %v", view)
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("selected text inspection changed original PDF bytes")
			}
		})
	}
}

func equalPDFCoveragePages(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestPDFTextBodyObserverTracksSplitUnicodeWithoutChangingBytes(t *testing.T) {
	for _, input := range []struct {
		body string
		text bool
	}{
		{body: " \t\r\n\f\u00a0\u3000\u2028\u2029"},
		{body: "\u3000\u2028真实正文", text: true},
		{body: "\u00a0[PDF page 7]", text: true},
	} {
		var forwarded bytes.Buffer
		observer := &agentWorkspacePDFBodyWriter{destination: &forwarded}
		for _, value := range []byte(input.body) {
			if _, err := observer.Write([]byte{value}); err != nil {
				t.Fatal(err)
			}
			if len(observer.pending) > 3 {
				t.Fatal("body observation retained an unbounded prefix")
			}
		}
		if observer.hasText != input.text || forwarded.String() != input.body {
			t.Errorf("body observation changed text or whitespace semantics: body=%q hasText=%t forwarded=%q", input.body, observer.hasText, forwarded.String())
		}
	}
}

func TestPDFTextCoverageFailedPageDoesNotBecomeEmpty(t *testing.T) {
	requirePDFRangeTools(t)
	converter, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.pdf")
	if err := os.WriteFile(path, selectedPDFTextCoverageFixture("", ""), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, finished, coverage := streamAgentWorkspacePDFText(ctx, converter, path, []int{1, 3})
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("native extraction did not reject an absent third page")
	}
	<-finished
	if coverage.hasText || len(coverage.textPages) != 0 || !equalPDFCoveragePages(coverage.noTextPages, []int{1}) {
		t.Fatalf("failed page was presented as a successful empty read: %+v", coverage)
	}
}

// Every page contains a real visible vector shape. Empty strings omit text
// drawing entirely; the real PDF readers, not a mocked extractor, decide what
// selectable text is present.
func selectedPDFTextCoverageFixture(pageBodies ...string) []byte {
	fontID := 3 + 2*len(pageBodies)
	kids := make([]string, len(pageBodies))
	for index := range kids {
		kids[index] = fmt.Sprintf("%d 0 R", 3+2*index)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageBodies)),
	}
	for index, body := range pageBodies {
		content := "q 0.3 0.5 0.7 rg 10 10 40 40 re f Q\n"
		if body != "" {
			content += "BT /F1 12 Tf 20 100 Td (" + body + ") Tj ET\n"
		}
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 160] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontID, 4+2*index),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		)
	}
	objects = append(objects, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return pdf.Bytes()
}
