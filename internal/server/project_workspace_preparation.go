package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspace "synon-go/internal/persistence/workspace"
)

func (s *Server) managedProjectPreparation(managed bool) workspace.ProjectMutationPreparation {
	if !managed {
		return nil
	}
	return func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		path, err := s.constrainManagedProjectWorkspaceMutationPath(project.ID, project.Path)
		if err != nil {
			return workspace.PreparedProjectResource{}, err
		}
		return prepareManagedProjectWorkspace(path)
	}
}

// Managed create/reset must prepare the exact server-derived project root. This
// does not constrain separately admitted host-grant or task workspace children.
func (s *Server) constrainManagedProjectWorkspaceMutationPath(projectID, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("managed project workspace path is not authorized")
	}
	managed, err := s.defaultAgentKernelWorkspace(projectID)
	if err != nil {
		return "", err
	}
	managed = filepath.Clean(managed)
	if filepath.Clean(path) != managed {
		return "", errors.New("managed project workspace path differs from the server-derived project root")
	}
	canonical, err := canonicalAgentWorkspaceDirectoryTarget(managed)
	if err != nil {
		return "", err
	}
	if canonical != managed {
		return "", errors.New("managed project workspace authority must not follow a symbolic path")
	}
	return managed, nil
}

// Preparation owns only a fresh, empty project directory. The shared parent
// namespace and any pre-existing directory are never compensation targets.
func prepareManagedProjectWorkspace(path string) (workspace.PreparedProjectResource, error) {
	if path == "" {
		return workspace.PreparedProjectResource{}, nil
	}
	target, err := canonicalAgentWorkspaceDirectoryTarget(path)
	if err != nil {
		return workspace.PreparedProjectResource{}, fmt.Errorf("prepare managed project workspace: %w", err)
	}
	if target != filepath.Clean(path) {
		return workspace.PreparedProjectResource{}, errors.New("managed project workspace authority must not follow a symbolic path")
	}
	parent, err := canonicalOrCreateAgentWorkspaceDirectory(filepath.Dir(target), 0o700)
	if err != nil {
		return workspace.PreparedProjectResource{}, fmt.Errorf("prepare managed project workspace: %w", err)
	}
	if parent != filepath.Dir(target) {
		return workspace.PreparedProjectResource{}, errors.New("managed project workspace parent changed; directory was not prepared")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			_ = root.Close()
		}
	}()
	parentInfo, err := root.Stat(".")
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	name := filepath.Base(target)
	created := true
	if err := root.Mkdir(name, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return workspace.PreparedProjectResource{}, fmt.Errorf("prepare managed project workspace: %w", err)
		}
		created = false
	}
	info, err := root.Lstat(name)
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return workspace.PreparedProjectResource{}, errors.New("managed project workspace must be a real directory")
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	heldInfo, err := directory.Stat(".")
	if err != nil || !os.SameFile(info, heldInfo) {
		_ = directory.Close()
		return workspace.PreparedProjectResource{}, errors.New("managed workspace changed while preparing; directory was retained")
	}
	resource := workspace.PreparedProjectResource{Release: func() {
		_ = directory.Close()
		_ = root.Close()
	}}
	if created {
		resource.Rollback = func() error {
			return removePreparedProjectDirectory(root, parent, name, parentInfo, heldInfo)
		}
	}
	handedOff = true
	return resource, nil
}

func removePreparedProjectDirectory(root *os.Root, parent, name string, parentInfo, createdInfo os.FileInfo) error {
	currentParent, err := os.Lstat(parent)
	if err != nil || currentParent.Mode()&os.ModeSymlink != 0 || !os.SameFile(parentInfo, currentParent) {
		return errors.New("prepared workspace parent changed; directory was not removed")
	}
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(createdInfo, current) || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepared workspace identity changed; directory was not removed")
	}
	// Held roots prevent inode reuse before this decision. Remove is still a
	// portable pathname operation, not an atomic compare-and-unlink primitive.
	// It is non-recursive: new contents are retained, never discarded.
	return root.Remove(name)
}
