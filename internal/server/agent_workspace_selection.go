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

// admitAgentWorkspace is the one filesystem admission path after durable frame
// identity has been established. Approval of a capability does not waive this
// path for an operation which actually mounts or mutates a task workspace.
func (s *Server) admitAgentWorkspace(access workspace.KernelFrameAccess, requested string) (string, error) {
	workdir, _, err := s.selectAgentWorkspace(access, requested)
	if err != nil {
		return "", err
	}
	workdir, err = s.materializeAdmittedManagedWorkspace(access.Frame.ProjectID, workdir)
	if err != nil {
		return "", err
	}
	return s.canonicalAgentWorkspaceRoot(&agentKernelContext{access: access, workspaceDir: workdir})
}
