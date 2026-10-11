package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kernelruntime "synon-go/internal/kernel"
)

var errProjectPathForbidden = errors.New("project path is not authorized")

func (s *Server) resolveAuthorizedProjectPath(userID, projectID, requested string, createManaged bool) (string, error) {
	userID = strings.TrimSpace(userID)
	projectID = strings.TrimSpace(projectID)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if !createManaged {
			return "", errors.New("project path is required")
		}
		if strings.TrimSpace(s.fileRoot) == "" {
			return "", nil
		}
		managed, err := s.defaultAgentKernelWorkspace(projectID)
		if err != nil {
			return "", err
		}
		created, err := canonicalOrCreateAgentWorkspaceDirectory(managed, 0o700)
		if err != nil {
			return "", fmt.Errorf("prepare managed project workspace: %w", err)
		}
		return created, nil
	}
	path, err := canonicalHostDirectory(requested)
	if err != nil {
		return "", err
	}
	if err := s.validateProjectPathAuthority(userID, projectID, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Server) validateProjectPathAuthority(userID, projectID, path string) error {
	if err := kernelruntime.ValidateHostMountPath(path); err != nil {
		return err
	}
	protectedPaths, err := s.agentKernelProtectedPaths()
	if err != nil {
		return errors.New("project path authorization could not be verified")
	}
	for _, protected := range protectedPaths {
		if hostPathWithin(protected, path) || hostPathWithin(path, protected) {
			return errors.New("project path overlaps protected application data")
		}
	}
	if err := s.rejectForeignManagedProjectWorkspace(projectID, path); err != nil {
		return err
	}
	managed, err := s.managedProjectWorkspacePath(projectID)
	if err == nil && hostPathWithin(managed, path) {
		return nil
	}
	if s.settingsStore == nil {
		return errProjectPathForbidden
	}
	grants, err := s.loadHostGrants(userID)
	if err != nil {
		return errors.New("project path authorization could not be verified")
	}
	for _, grant := range grants {
		if grant.Mode != "read_write" {
			continue
		}
		root, resolveErr := canonicalHostDirectory(grant.Path)
		if resolveErr != nil {
			continue
		}
		if err := kernelruntime.ValidateHostMountPath(root); err != nil {
			continue
		}
		if hostPathWithin(root, path) {
			return nil
		}
	}
	return errProjectPathForbidden
}

func (s *Server) managedProjectWorkspacePath(projectID string) (string, error) {
	managed, err := s.defaultAgentKernelWorkspace(projectID)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(managed); statErr == nil && info.IsDir() {
		return canonicalHostDirectory(managed)
	}
	return filepath.Clean(managed), nil
}

func (s *Server) rejectForeignManagedProjectWorkspace(projectID, path string) error {
	if s == nil || strings.TrimSpace(s.fileRoot) == "" {
		return nil
	}
	fileRoot, err := canonicalHostDirectory(s.fileRoot)
	if err != nil {
		return errors.New("project path authorization could not be verified")
	}
	workspacesRoot := filepath.Clean(fileRoot + "-workspaces")
	if !hostPathWithin(workspacesRoot, path) {
		return nil
	}
	managed, err := s.managedProjectWorkspacePath(projectID)
	if err != nil {
		return errors.New("project path overlaps a managed workspace reserved for another project")
	}
	if hostPathWithin(managed, path) {
		return nil
	}
	return errors.New("project path overlaps a managed workspace reserved for another project")
}

func (s *Server) authorizedProjectWebFSRoot(userID, projectID, projectPath string) (webFSRoot, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return webFSRoot{}, false
	}
	path, err := canonicalWebFSDirectory(projectPath)
	if err != nil {
		return webFSRoot{}, false
	}
	if err := s.validateProjectPathAuthority(userID, projectID, path); err != nil {
		return webFSRoot{}, false
	}
	managed, managedErr := s.managedProjectWorkspacePath(projectID)
	if managedErr == nil && hostPathWithin(managed, path) {
		return webFSRoot{Path: path, Writable: true}, true
	}
	if s.settingsStore != nil {
		grants, err := s.loadHostGrants(userID)
		if err == nil {
			for _, grant := range grants {
				root, resolveErr := canonicalHostDirectory(grant.Path)
				if resolveErr != nil || !hostPathWithin(root, path) {
					continue
				}
				return webFSRoot{Path: path, Writable: grant.Mode == "read_write"}, true
			}
		}
	}
	return webFSRoot{}, false
}
