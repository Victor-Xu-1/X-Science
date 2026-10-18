package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type pdfDocumentInspection struct {
	PageCount int
	Encrypted bool
}

type pdfDocumentInspectionError struct{ reason string }

func (err *pdfDocumentInspectionError) Error() string { return err.reason }

// Both reading bindings and file reads use the same backend-host capability.
// A missing inspector is not a storage failure or a zero-page document.
func pdfDocumentInspector() (string, error) {
	path, err := exec.LookPath("pdfinfo")
	if err != nil {
		return "", &pdfDocumentInspectionError{"inspection_unavailable"}
	}
	return path, nil
}

// path is a private snapshot staged from an already-authorized open handle.
// Never use a user-supplied path or reopen the mutable original for metadata.
func inspectStagedPDFDocument(ctx context.Context, path string) (pdfDocumentInspection, error) {
	var inspection pdfDocumentInspection
	inspector, err := pdfDocumentInspector()
	if err != nil {
		return inspection, err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, inspector, path)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	command.WaitDelay = time.Second
	stdout, stderr := &agentWorkspaceBoundedCapture{limit: 16 << 10}, &agentWorkspaceBoundedCapture{limit: 4 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return inspection, cause
		}
		if bounded.Err() != nil {
			return inspection, &pdfDocumentInspectionError{"inspection_timeout"}
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if strings.Contains(strings.ToLower(stderr.String()), "incorrect password") {
				return inspection, &pdfDocumentInspectionError{"encrypted_pdf"}
			}
			return inspection, &pdfDocumentInspectionError{"invalid_pdf"}
		}
		return inspection, &pdfDocumentInspectionError{"inspection_unavailable"}
	}
	seenPages := false
	for _, line := range strings.Split(stdout.String(), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages":
			// Metadata text must not be able to spoof a second authoritative
			// Pages field in the inspector output.
			if seenPages {
				return pdfDocumentInspection{}, &pdfDocumentInspectionError{"inspection_unavailable"}
			}
			seenPages = true
			inspection.PageCount, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || inspection.PageCount <= 0 {
				return pdfDocumentInspection{}, &pdfDocumentInspectionError{"inspection_unavailable"}
			}
		case "Encrypted":
			inspection.Encrypted = strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "yes")
		}
	}
	if !seenPages || stdout.truncated {
		return pdfDocumentInspection{}, &pdfDocumentInspectionError{"inspection_unavailable"}
	}
	return inspection, nil
}
