package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	workspace "synon-go/internal/persistence/workspace"
)

type managedProjectWorkspacePreparation struct {
	path         string
	createdFresh bool
	committed    bool
}

func (s *Server) prepareManagedProjectWorkspaceForMutation(ctx context.Context, store *workspace.Store, projectID, path string) (*managedProjectWorkspacePreparation, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("managed project workspace path is required")
	}
	if err := s.validateManagedProjectWorkspaceMutationPath(projectID, path); err != nil {
		return nil, err
	}
	existedBefore, err := managedProjectWorkspaceExisted(path)
	if err != nil {
		return nil, err
	}
	if _, err := s.materializeManagedProjectWorkspace(path); err != nil {
		return nil, err
	}
	return &managedProjectWorkspacePreparation{path: path, createdFresh: !existedBefore}, nil
}

func (s *Server) validateManagedProjectWorkspaceMutationPath(projectID, path string) error {
	managed, err := s.managedProjectWorkspacePath(projectID)
	if err != nil {
		return err
	}
	target, err := canonicalAgentWorkspaceDirectoryTarget(path)
	if err != nil {
		return err
	}
	if !hostPathWithin(managed, target) {
		return errors.New("managed project workspace path is not authorized")
	}
	return nil
}

func managedProjectWorkspaceExisted(path string) (bool, error) {
	target, err := canonicalAgentWorkspaceDirectoryTarget(path)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func (prep *managedProjectWorkspacePreparation) Commit() {
	if prep != nil {
		prep.committed = true
	}
}

func (prep *managedProjectWorkspacePreparation) Compensate(ctx context.Context, store *workspace.Store, projectID string) error {
	if prep == nil || prep.committed || !prep.createdFresh {
		return nil
	}
	if store == nil {
		return errors.New("workspace store is required to compensate managed project workspace")
	}
	refs, err := store.CountProjectPathDurableReferences(ctx, prep.path, projectID)
	if err != nil {
		return err
	}
	if refs > 0 {
		return nil
	}
	target, err := canonicalAgentWorkspaceDirectoryTarget(prep.path)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect managed project workspace before compensation: %w", err)
	}
	if len(entries) != 0 {
		return nil
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("compensate managed project workspace: %w", err)
	}
	return nil
}
