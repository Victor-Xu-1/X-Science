package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

type managedProjectPreparationOptions struct {
	AllowExisting bool
}

type managedProjectPreparationOps struct {
	openChild func(*os.Root, string) (*os.Root, error)
	openPin   func(*os.Root, string) (*os.File, error)
}

func (s *Server) managedProjectPreparation(managed bool, options ...managedProjectPreparationOptions) workspace.ProjectMutationPreparation {
	if !managed {
		return nil
	}
	return func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		path, err := s.constrainManagedProjectWorkspaceMutationPath(project.ID, project.Path)
		if err != nil {
			return workspace.PreparedProjectResource{}, err
		}
		return prepareManagedProjectWorkspace(path, options...)
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
func prepareManagedProjectWorkspace(path string, options ...managedProjectPreparationOptions) (workspace.PreparedProjectResource, error) {
	option := managedProjectPreparationOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	return prepareManagedProjectWorkspaceWithOps(path, option, managedProjectPreparationOps{})
}

func prepareManagedProjectWorkspaceWithOps(path string, options managedProjectPreparationOptions, ops managedProjectPreparationOps) (workspace.PreparedProjectResource, error) {
	if ops.openChild == nil {
		ops.openChild = func(root *os.Root, name string) (*os.Root, error) { return root.OpenRoot(name) }
	}
	if ops.openPin == nil {
		ops.openPin = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	}
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
	// Reserve the native pin and child-root descriptor capacity before mkdir.
	// Static descriptor exhaustion therefore creates no orphan. Reservation is
	// not a guarantee against another goroutine taking a released slot.
	reservedPin, err := ops.openPin(root, ".")
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	defer func() {
		if reservedPin != nil {
			_ = reservedPin.Close()
		}
	}()
	reservedRoot, err := ops.openChild(root, ".")
	if err != nil {
		return workspace.PreparedProjectResource{}, err
	}
	defer func() {
		if reservedRoot != nil {
			_ = reservedRoot.Close()
		}
	}()
	name := filepath.Base(target)
	created := true
	if err := root.Mkdir(name, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return workspace.PreparedProjectResource{}, fmt.Errorf("prepare managed project workspace: %w", err)
		}
		if !options.AllowExisting {
			return workspace.PreparedProjectResource{}, errors.New("managed project workspace already exists; refusing unowned retained directory")
		}
		created = false
	}
	owned := &preparedProjectDirectory{
		root: root, parent: parent, name: name, parentInfo: parentInfo, created: created,
	}
	resource := owned.resource()
	handedOff = true
	_ = reservedPin.Close()
	reservedPin = nil
	info, err := root.Lstat(name)
	if err != nil {
		return resource, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return resource, errors.New("managed project workspace must be a real directory")
	}
	owned.pin, err = ops.openPin(root, name)
	if err != nil {
		return resource, err
	}
	pinnedInfo, err := owned.pin.Stat()
	if err != nil || !pinnedInfo.IsDir() || !os.SameFile(info, pinnedInfo) {
		return resource, errors.New("managed workspace creation identity could not be verified; directory was retained")
	}
	owned.createdInfo = pinnedInfo
	_ = reservedRoot.Close()
	reservedRoot = nil
	directory, err := ops.openChild(root, name)
	if err != nil {
		return resource, err
	}
	owned.directory = directory
	heldInfo, err := directory.Stat(".")
	if err != nil || !os.SameFile(pinnedInfo, heldInfo) {
		return resource, errors.New("managed workspace changed while preparing; directory was retained")
	}
	// Transfer inode observation to the validated child root without a gap.
	_ = owned.pin.Close()
	owned.pin = nil
	return resource, nil
}

type preparedProjectDirectory struct {
	root, directory         *os.Root
	pin                     *os.File
	parent, name            string
	parentInfo, createdInfo os.FileInfo
	created                 bool
}

func (owned *preparedProjectDirectory) resource() workspace.PreparedProjectResource {
	resource := workspace.PreparedProjectResource{Release: func() {
		if owned.pin != nil {
			_ = owned.pin.Close()
		}
		if owned.directory != nil {
			_ = owned.directory.Close()
		}
		_ = owned.root.Close()
	}}
	if owned.created {
		resource.Rollback = func() error {
			if owned.createdInfo == nil || (owned.pin == nil && owned.directory == nil) {
				return errors.New("prepared workspace creation identity could not be verified; directory was retained")
			}
			return removePreparedProjectDirectory(owned.root, owned.parent, owned.name, owned.parentInfo, owned.createdInfo)
		}
	}
	return resource
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
