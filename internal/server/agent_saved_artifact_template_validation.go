package server

import (
	"context"
	"io"
	"os"
)

// Working data retains source syntax and bytes. User-facing snapshots also
// receive report-link normalization and template-quality observations.
func agentSavedArtifactRequiresRenderedBytes(retention string) bool {
	return retention != "working_data"
}

func (s *Server) prepareAgentSavedArtifactReportBytes(ctx context.Context, relativePath, projectID, retention string, snapshot *os.File) (bool, error) {
	if !agentSavedArtifactRequiresRenderedBytes(retention) {
		return false, nil
	}
	normalized, err := s.normalizeAgentSavedArtifactReferences(ctx, snapshot, relativePath, projectID)
	if err != nil {
		return false, err
	}
	return normalized, validateAgentSavedArtifactTemplates(ctx, relativePath, snapshot)
}

// Keep exact checker findings so the save owner can distinguish a template
// quality observation from a malformed artifact reference or a read failure.
func validateAgentSavedArtifactTemplates(ctx context.Context, relativePath string, snapshot *os.File) error {
	if snapshot == nil {
		return nil
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		return err
	}
	failures, err := scanRunnerTemplateFailures(ctx, snapshot, relativePath)
	if err != nil {
		return err
	}
	if _, seekErr := snapshot.Seek(0, io.SeekStart); seekErr != nil {
		return seekErr
	}
	if len(failures) == 0 {
		return nil
	}
	return &agentSavedArtifactTemplateFindings{failures: append([]string(nil), failures...)}
}
