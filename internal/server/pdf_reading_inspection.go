package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type pdfReadingCandidateError struct{ reason string }

func (err *pdfReadingCandidateError) Error() string { return err.reason }

func (s *Server) inspectPDFReadingSource(ctx context.Context, owner, project string, source transcriptstore.PDFReadingSource) (transcriptstore.PDFReadingBinding, error) {
	var binding transcriptstore.PDFReadingBinding
	artifact, version, reader, found, err := s.workspaceStore.OpenArtifactVersionContentForOwner(source.VersionID, owner)
	if errors.Is(err, workspace.ErrArtifactContentPruned) || errors.Is(err, os.ErrNotExist) {
		return binding, &pdfReadingCandidateError{"unavailable"}
	}
	if err != nil {
		return binding, err
	}
	if !found {
		return binding, &pdfReadingCandidateError{"unavailable"}
	}
	defer reader.Close()
	if artifact.ID != source.ArtifactID || artifact.ProjectID != project {
		return binding, &pdfReadingCandidateError{"unavailable"}
	}
	if version.SizeBytes > transcriptstore.MaxPDFReadingBytes {
		return binding, &pdfReadingCandidateError{"too_large"}
	}
	if version.SizeBytes <= 0 {
		return binding, &pdfReadingCandidateError{"invalid_pdf"}
	}
	content, err := io.ReadAll(io.LimitReader(reader, transcriptstore.MaxPDFReadingBytes+1))
	if err != nil {
		return binding, err
	}
	sum := sha256.Sum256(content)
	if int64(len(content)) != version.SizeBytes || fmt.Sprintf("%x", sum) != strings.ToLower(version.ContentSHA256) {
		return binding, &pdfReadingCandidateError{"integrity_unavailable"}
	}
	if !bytes.HasPrefix(content, []byte("%PDF-")) {
		return binding, &pdfReadingCandidateError{"not_pdf"}
	}
	path, cleanup, err := stageAgentWorkspacePDFSource(ctx, bytes.NewReader(content), version.SizeBytes)
	if err != nil {
		return binding, err
	}
	defer cleanup()
	inspection, err := inspectStagedPDFDocument(ctx, path)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return binding, cause
		}
		var unavailable *pdfDocumentInspectionError
		if !errors.As(err, &unavailable) {
			return binding, err
		}
		reason := unavailable.reason
		if reason == "encrypted_pdf" {
			reason = "invalid_pdf"
		} else if reason == "inspection_timeout" {
			reason = "inspection_unavailable"
		}
		return binding, &pdfReadingCandidateError{reason}
	}
	if inspection.Encrypted {
		return binding, &pdfReadingCandidateError{"invalid_pdf"}
	}
	if inspection.PageCount == 1 {
		return binding, &pdfReadingCandidateError{"single_page"}
	}
	return transcriptstore.PDFReadingBinding{BindingID: "inspected", SourceKind: "artifact-version", ArtifactID: artifact.ID, VersionID: version.ID,
		Name: artifact.Name, MIMEType: "application/pdf", SizeBytes: version.SizeBytes, Checksum: strings.ToLower(version.ContentSHA256),
		PageCount: inspection.PageCount, LinkedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}
