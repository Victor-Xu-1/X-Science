package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// These controlled dependency responses cover the parser/error contract. The
// page-reading regressions separately execute real Poppler on real PDF bytes.
func TestPDFDocumentInspectionRejectsAmbiguousMetadataAndSeparatesEncryption(t *testing.T) {
	for _, test := range []struct {
		name, script, reason string
		pages                int
		encrypted            bool
	}{
		{"verified", "printf 'Pages: 2\\nEncrypted: no\\n'", "", 2, false},
		{"duplicate", "printf 'Pages: 999\\nPages: 2\\n'", "inspection_unavailable", 0, false},
		{"malformed", "printf 'Pages: unknown\\n'", "inspection_unavailable", 0, false},
		{"missing", "printf 'Title: untrusted metadata\\n'", "inspection_unavailable", 0, false},
		{"password", "printf 'Command Line Error: Incorrect password\\n' >&2; exit 1", "encrypted_pdf", 0, false},
		{"encrypted-metadata", "printf 'Pages: 2\\nEncrypted: yes (print:no)\\n'", "", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools := t.TempDir()
			if err := os.WriteFile(filepath.Join(tools, "pdfinfo"), []byte("#!/bin/sh\n"+test.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools)
			raw := minimalAgentWorkspacePDF()
			path, cleanup, err := stageAgentWorkspacePDFSource(context.Background(), bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			inspection, err := inspectStagedPDFDocument(context.Background(), path)
			if test.reason != "" {
				var unavailable *pdfDocumentInspectionError
				if !errors.As(err, &unavailable) || unavailable.reason != test.reason || inspection.PageCount != 0 {
					t.Fatalf("inspection reason/count=%#v/%v, want %s", inspection, err, test.reason)
				}
				return
			}
			if err != nil || inspection.PageCount != test.pages || inspection.Encrypted != test.encrypted {
				t.Fatalf("inspection=%#v err=%v", inspection, err)
			}
			if test.encrypted {
				result, err := readAgentWorkspacePDF(context.Background(), bytes.NewReader(raw), "encrypted.pdf", "application/pdf", int64(len(raw)), []int{1}, map[string]any{"pages": []int{1}})
				if err != nil || mapValue(result)["code"] != "pdf_encrypted" || mapValue(result)["executed"] != false || toolResultProvidesExecutedEvidence(result) {
					t.Fatalf("encrypted page read invented content or lost its typed outcome: %#v err=%v", result, err)
				}
			}
		})
	}
}
