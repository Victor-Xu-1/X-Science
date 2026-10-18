package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"x-science/internal/agentruntime"
)

func requirePDFRangeTools(t *testing.T) {
	t.Helper()
	for _, name := range []string{"pdfinfo", "pdftotext", "pdftoppm"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("real PDF regression requires %s: %v", name, err)
		}
	}
}

func TestPDFReadMissingInspectorPreservesReadableContentWithoutInventingCount(t *testing.T) {
	requirePDFRangeTools(t)
	// Retain the actual readers but deliberately remove the optional inspector.
	tools := t.TempDir()
	for _, name := range []string{"pdftotext", "pdftoppm"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	raw := minimalAgentWorkspacePDF()
	for _, input := range []map[string]any{nil, {"offset": 1}, {"pages": []int{1}}} {
		result, err := readAgentWorkspaceFile(context.Background(), bytes.NewReader(raw), "source.pdf", "application/pdf", int64(len(raw)), input)
		if err != nil {
			t.Fatal(err)
		}
		value, _ := unwrapAgentRuntimeRichToolResponse(result)
		view := mapValue(value)
		if _, invented := view["total_pages"]; invented || view["page_count_status"] != "unavailable" || view["page_count_reason"] != "inspection_unavailable" || agentruntime.ClassifyToolResult(view) != agentruntime.ToolResultSucceeded {
			t.Fatalf("missing optional capability blocked reading or invented a bound: %#v", view)
		}
	}
}

func TestPDFReadInvalidDocumentIsNotAnOutOfRangeRequest(t *testing.T) {
	requirePDFRangeTools(t)
	raw := []byte("%PDF-1.4\ninvalid-document\n")
	result, err := readAgentWorkspaceFile(context.Background(), bytes.NewReader(raw), "broken.pdf", "application/pdf", int64(len(raw)), map[string]any{"pages": []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	value, parts := unwrapAgentRuntimeRichToolResponse(result)
	view := mapValue(value)
	if _, invented := view["total_pages"]; invented || view["code"] != "pdf_invalid" || view["page_count_reason"] != "invalid_pdf" || len(parts) != 0 || !agentruntime.IsNonExecutingPreflight(view) || toolResultProvidesExecutedEvidence(view) {
		t.Fatalf("invalid document was mislabeled as a page-bound or successful read: %#v", view)
	}
}

func TestPDFReadPageInspectionPreservesParentCancellation(t *testing.T) {
	requirePDFRangeTools(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := inspectStagedPDFDocument(ctx, filepath.Join(t.TempDir(), "unused.pdf"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("inspection swallowed parent cancellation: %v", err)
	}
}

func TestPDFReadExposesVerifiedPageCountAcrossVisualAndText(t *testing.T) {
	requirePDFRangeTools(t)
	raw := minimalAgentWorkspacePDF()
	inputs := []map[string]any{nil, {"offset": 1, "limit": 1}, {"pages": []int{1}}}
	for index, input := range inputs {
		result, err := readAgentWorkspaceFile(context.Background(), bytes.NewReader(raw), "source.pdf", "application/pdf", int64(len(raw)), input)
		if err != nil {
			t.Errorf("read mode %d failed: %v", index, err)
			continue
		}
		value, _ := unwrapAgentRuntimeRichToolResponse(result)
		view := mapValue(value)
		if numberValue(view["total_pages"]) != 1 || view["page_count_status"] != "verified" {
			t.Errorf("read mode %d hides the verified page bound: count=%v status=%v", index, view["total_pages"], view["page_count_status"])
		}
	}
}

func TestPDFReadRangeReturnsTypedBoundsWithoutLosingValidPages(t *testing.T) {
	requirePDFRangeTools(t)
	raw := minimalAgentWorkspacePDF()
	for _, pages := range [][]int{{2}, {1, 2}} {
		result, err := readAgentWorkspaceFile(context.Background(), bytes.NewReader(raw), "source.pdf", "application/pdf", int64(len(raw)), map[string]any{"pages": pages})
		if err != nil {
			t.Errorf("page scope %v returned an opaque render error instead of exact bounds: %v", pages, err)
			continue
		}
		value, parts := unwrapAgentRuntimeRichToolResponse(result)
		view := mapValue(value)
		invalid := agentWorkspacePageValues(view["invalid_pages"])
		if numberValue(view["total_pages"]) != 1 || len(invalid) != 1 || invalid[0] != 2 {
			t.Errorf("page scope %v lost verified total/invalid-page facts: %#v", pages, view)
		}
		if len(pages) == 1 {
			if len(parts) != 0 || view["code"] != "pdf_page_out_of_range" || !agentruntime.IsNonExecutingPreflight(view) || toolResultProvidesExecutedEvidence(view) {
				t.Errorf("all-invalid pages invented reading or lost the typed preflight: %#v", view)
			}
		} else if len(parts) != 1 || parts[0].Type != agentruntime.ContentPartImage ||
			agentruntime.ClassifyToolResult(view) != agentruntime.ToolResultPartial || toolResultProvidesExecutedEvidence(view) {
			t.Errorf("mixed page scope did not preserve only the valid image as partial evidence: parts=%d view=%#v", len(parts), view)
		}
	}
}
