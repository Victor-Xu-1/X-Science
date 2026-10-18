package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
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
	inspector, err := exec.LookPath("pdfinfo")
	if err != nil {
		return binding, errors.New("governed PDF inspector is not installed")
	}
	path, cleanup, err := stageAgentWorkspacePDFSource(ctx, bytes.NewReader(content), version.SizeBytes)
	if err != nil {
		return binding, err
	}
	defer cleanup()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, inspector, path)
	stdout, stderr := &agentWorkspaceBoundedCapture{limit: 16 << 10}, &agentWorkspaceBoundedCapture{limit: 4 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err = command.Run(); err != nil {
		if bounded.Err() != nil {
			return binding, bounded.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return binding, &pdfReadingCandidateError{"invalid_pdf"}
		}
		return binding, err
	}
	pages := 0
	for _, line := range strings.Split(stdout.String(), "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "Pages" {
			pages, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return binding, &pdfReadingCandidateError{"inspection_unavailable"}
			}
		}
	}
	if pages <= 0 || stdout.truncated {
		return binding, &pdfReadingCandidateError{"inspection_unavailable"}
	}
	if pages == 1 {
		return binding, &pdfReadingCandidateError{"single_page"}
	}
	return transcriptstore.PDFReadingBinding{BindingID: "inspected", SourceKind: "artifact-version", ArtifactID: artifact.ID, VersionID: version.ID,
		Name: artifact.Name, MIMEType: "application/pdf", SizeBytes: version.SizeBytes, Checksum: strings.ToLower(version.ContentSHA256),
		PageCount: pages, LinkedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}
