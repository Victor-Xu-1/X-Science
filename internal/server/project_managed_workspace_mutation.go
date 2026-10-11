package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspace "synon-go/internal/persistence/workspace"
)

type managedProjectWorkspacePreparation struct {
	path           string
	workspacesRoot string
	createdFresh   bool
	committed      bool
}

func (s *Server) prepareManagedProjectWorkspaceForMutation(ctx context.Context, store *workspace.Store, projectID, path string) (*managedProjectWorkspacePreparation, error) {
	contained, workspacesRoot, err := s.constrainManagedProjectWorkspaceMutationPath(projectID, path)
	if err != nil {
		return nil, err
	}
	existedBefore, err := managedProjectWorkspaceExistedUnderRoot(workspacesRoot, contained)
	if err != nil {
		return nil, err
	}
	if _, err := s.materializeManagedProjectWorkspace(contained); err != nil {
		return nil, err
	}
	return &managedProjectWorkspacePreparation{path: contained, workspacesRoot: workspacesRoot, createdFresh: !existedBefore}, nil
}

// constrainManagedProjectWorkspaceMutationPath proves the caller path stays
// inside this project's managed workspace, then returns the server-derived
// managed path reconstructed under the authorized workspaces root. Filesystem
// mutation never uses the raw caller expression.
func (s *Server) constrainManagedProjectWorkspaceMutationPath(projectID, path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", errors.New("managed project workspace path is required")
	}
	workspacesRoot, err := s.managedProjectWorkspacesRoot()
	if err != nil {
		return "", "", err
	}
	managed, err := s.managedProjectWorkspacePath(projectID)
	if err != nil {
		return "", "", err
	}
	managed = filepath.Clean(managed)
	target, err := canonicalAgentWorkspaceDirectoryTarget(path)
	if err != nil {
		return "", "", err
	}
	target = filepath.Clean(target)
	if !hostPathWithin(managed, target) {
		return "", "", errors.New("managed project workspace path is not authorized")
	}
	contained, err := containedPathJoin(workspacesRoot, managed)
	if err != nil {
		return "", "", err
	}
	if !hostPathWithin(managed, contained) {
		return "", "", errors.New("managed project workspace path is not authorized")
	}
	return contained, workspacesRoot, nil
}

func (s *Server) managedProjectWorkspacesRoot() (string, error) {
	if s == nil || strings.TrimSpace(s.fileRoot) == "" {
		return "", errors.New("managed project workspace path is not authorized")
	}
	fileRoot, err := canonicalHostDirectory(s.fileRoot)
	if err != nil {
		return "", errors.New("managed project workspace path is not authorized")
	}
	root := filepath.Clean(fileRoot + "-workspaces")
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	return root, nil
}

func containedPathJoin(root, target string) (string, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	contained := filepath.Join(root, relative)
	if contained == root || !strings.HasPrefix(contained, root+string(filepath.Separator)) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	return contained, nil
}

func managedProjectWorkspaceExistedUnderRoot(workspacesRoot, path string) (bool, error) {
	relative, err := authorizedManagedWorkspaceRelative(workspacesRoot, path)
	if err != nil {
		return false, err
	}
	held, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer held.Close()
	info, err := held.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func authorizedManagedWorkspaceRelative(workspacesRoot, path string) (string, error) {
	workspacesRoot = filepath.Clean(workspacesRoot)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(workspacesRoot, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	if !strings.HasPrefix(path, workspacesRoot+string(filepath.Separator)) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	return relative, nil
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
	relative, err := authorizedManagedWorkspaceRelative(prep.workspacesRoot, prep.path)
	if err != nil {
		return err
	}
	held, err := os.OpenRoot(prep.workspacesRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect managed project workspace before compensation: %w", err)
	}
	defer held.Close()
	directory, err := held.OpenRoot(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect managed project workspace before compensation: %w", err)
	}
	file, err := directory.Open(".")
	if err != nil {
		_ = directory.Close()
		return fmt.Errorf("inspect managed project workspace before compensation: %w", err)
	}
	entries, err := file.ReadDir(-1)
	_ = file.Close()
	_ = directory.Close()
	if err != nil {
		return fmt.Errorf("inspect managed project workspace before compensation: %w", err)
	}
	if len(entries) != 0 {
		return nil
	}
	if err := held.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("compensate managed project workspace: %w", err)
	}
	return nil
}
