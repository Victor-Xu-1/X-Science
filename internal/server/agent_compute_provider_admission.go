package server

import (
	"context"
	"errors"

	workspace "synon-go/internal/persistence/workspace"
)

// admitAgentComputeProviderWorkspace uses the same current frame and filesystem
// authority at both physical registration and final execution handoff. The
// action is bounded startup/queue admission, never a handshake or long job.
func (s *Server) admitAgentComputeProviderWorkspace(ctx context.Context, bound workspace.KernelFrameAccess, expectedWorkspace string, admit func() error) error {
	return s.withKernelHostGrantAdmission(bound.UserID, func() error {
		current, err := s.validateKernelHostIdentity(ctx, bound)
		if err != nil {
			return err
		}
		actual, err := s.admitAgentWorkspace(current, current.ProjectPath)
		if err != nil {
			return err
		}
		if actual != expectedWorkspace {
			return errors.New("compute provider workspace authority changed before admission")
		}
		return admit()
	})
}
