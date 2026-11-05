package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Working data retains source syntax and bytes. Only user-facing snapshots
// require report-link normalization and fully materialized template fields.
// Unknown or legacy retention modes keep the strict report policy.
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

// validateAgentSavedArtifactTemplates enforces the final-bytes contract at the
// publication boundary. Completion review keeps the same check as defense in
// depth, but a model receives an actionable save_artifacts failure before any
// invalid version becomes canonical.
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
	return fmt.Errorf("%w: %s", errAgentSavedArtifactTemplateUnresolved, strings.Join(failures, ", "))
}
