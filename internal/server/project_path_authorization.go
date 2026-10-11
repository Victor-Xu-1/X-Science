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

type hostPathCoverage struct {
	writable bool
}

func (s *Server) resolveAuthorizedProjectPath(userID, projectID, requested string, createManaged bool) (string, bool, error) {
	userID = strings.TrimSpace(userID)
	projectID = strings.TrimSpace(projectID)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if !createManaged {
			return "", false, errors.New("project path is required")
		}
		if strings.TrimSpace(s.fileRoot) == "" {
			return "", false, nil
		}
		managed, err := s.defaultAgentKernelWorkspace(projectID)
		if err != nil {
			return "", false, err
		}
		// Persist first; materializeManagedProjectWorkspace creates the inode
		// only after the project row exists.
		return filepath.Clean(managed), true, nil
	}
	path, err := canonicalHostDirectory(requested)
	if err != nil {
		return "", false, err
	}
	if err := s.validateProjectPathAuthority(userID, projectID, path); err != nil {
		return "", false, err
	}
	return path, false, nil
}

func (s *Server) materializeManagedProjectWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	created, err := canonicalOrCreateAgentWorkspaceDirectory(path, 0o700)
	if err != nil {
		return "", fmt.Errorf("prepare managed project workspace: %w", err)
	}
	return created, nil
}

// materializeAdmittedManagedWorkspace creates this project's managed workspace
// inode after admission (project or task path). Host-granted paths are left
// unchanged so grant authorization never creates directories.
func (s *Server) materializeAdmittedManagedWorkspace(projectID, workdir string) (string, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return "", nil
	}
	managed, err := s.managedProjectWorkspacePath(projectID)
	if err != nil {
		return workdir, nil
	}
	target, err := canonicalAgentWorkspaceDirectoryTarget(workdir)
	if err != nil {
		return "", err
	}
	if !hostPathWithin(managed, target) {
		return workdir, nil
	}
	return s.materializeManagedProjectWorkspace(target)
}

func (s *Server) validateProjectPathAuthority(userID, projectID, path string) error {
	return s.validateProjectPathAdmission(userID, projectID, path, true)
}

func (s *Server) validateProjectPathAdmission(userID, projectID, path string, requireWrite bool) error {
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
	coverage, covered, err := s.coveringHostPathGrant(userID, path)
	if err != nil {
		return errors.New("project path authorization could not be verified")
	}
	if !covered || (requireWrite && !coverage.writable) {
		return errProjectPathForbidden
	}
	return nil
}

func (s *Server) coveringHostPathGrant(userID, path string) (hostPathCoverage, bool, error) {
	if s == nil || s.settingsStore == nil {
		return hostPathCoverage{}, false, nil
	}
	grants, err := s.loadHostGrants(userID)
	if err != nil {
		return hostPathCoverage{}, false, err
	}
	covered := false
	writable := false
	for _, grant := range grants {
		if grant.Mode != "read" && grant.Mode != "read_write" {
			continue
		}
		root, resolveErr := canonicalHostDirectory(grant.Path)
		if resolveErr != nil {
			continue
		}
		if err := kernelruntime.ValidateHostMountPath(root); err != nil {
			continue
		}
		if !hostPathWithin(root, path) {
			continue
		}
		covered = true
		if grant.Mode == "read_write" {
			writable = true
		}
	}
	return hostPathCoverage{writable: writable}, covered, nil
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
	if err := s.validateProjectPathAdmission(userID, projectID, path, false); err != nil {
		return webFSRoot{}, false
	}
	managed, managedErr := s.managedProjectWorkspacePath(projectID)
	if managedErr == nil && hostPathWithin(managed, path) {
		return webFSRoot{Path: path, Writable: true}, true
	}
	coverage, covered, err := s.coveringHostPathGrant(userID, path)
	if err != nil || !covered {
		return webFSRoot{}, false
	}
	return webFSRoot{Path: path, Writable: coverage.writable}, true
}

// liveWritableProjectWorkspace re-checks persisted project.Path against the
// current write grant or this project's managed workspace. Revoked or
// read-only host paths must not become a writable kernel workdir.
func (s *Server) liveWritableProjectWorkspace(userID, projectID, workdir string) (string, bool) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" || !filepath.IsAbs(workdir) {
		return "", false
	}
	target, err := canonicalAgentWorkspaceDirectoryTarget(workdir)
	if err != nil {
		return "", false
	}
	if err := s.validateProjectPathAdmission(userID, projectID, target, true); err != nil {
		return "", false
	}
	return target, true
}
