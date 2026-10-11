package server

import (
	"strings"

	workspace "synon-go/internal/persistence/workspace"
)

// selectAgentWorkspace shares the existing project/grant authority between
// attachment materialization and execution. A stored managed project path is
// an ownership namespace, not a shared task cwd. Only an admitted external
// host workspace retains the explicit repository-path contract.
func (s *Server) selectAgentWorkspace(access workspace.KernelFrameAccess, requested string) (string, bool, error) {
	managedRoot, err := s.managedProjectWorkspacePath(access.Frame.ProjectID)
	if err != nil {
		return "", false, err
	}
	if admitted, ok := s.liveWritableProjectWorkspace(access.UserID, access.Frame.ProjectID, strings.TrimSpace(requested)); ok &&
		!hostPathWithin(managedRoot, admitted) {
		return admitted, false, nil
	}
	taskRoot, err := s.defaultAgentKernelTaskWorkspace(
		access.Frame.ProjectID, access.Frame.RootFrameID, access.RootFrameIncarnationID,
	)
	if err != nil {
		return "", false, err
	}
	return taskRoot, true, nil
}
