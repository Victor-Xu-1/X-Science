package server

import (
	"context"
	"errors"
	"strings"

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
		fromProject, err := s.admitAgentWorkspace(current, current.ProjectPath)
		if err != nil {
			return err
		}
		requested := strings.TrimSpace(expectedWorkspace)
		if requested == "" {
			requested = current.ProjectPath
		}
		fromBinding, err := s.admitAgentWorkspace(current, requested)
		if err != nil {
			return err
		}
		if fromProject != fromBinding {
			return errors.New("compute provider workspace authority changed before admission")
		}
		return admit()
	})
}

// reconcileAgentComputeProviderWorkspace returns the admitted cwd for provider
// startup. A persisted project.Path without a live grant remaps to the managed
// task workspace; the binding must still match that admission at handoff.
func (s *Server) reconcileAgentComputeProviderWorkspace(access workspace.KernelFrameAccess, bound string) (string, error) {
	fromProject, err := s.admitAgentWorkspace(access, access.ProjectPath)
	if err != nil {
		return "", err
	}
	requested := strings.TrimSpace(bound)
	if requested == "" {
		return fromProject, nil
	}
	fromBinding, err := s.admitAgentWorkspace(access, requested)
	if err != nil {
		return "", err
	}
	if fromProject != fromBinding {
		return "", errors.New("compute provider task workspace is unavailable")
	}
	return fromProject, nil
}
