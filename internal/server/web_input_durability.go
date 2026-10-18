package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func webInputPublicationError(err error) error {
	var uncertain *workspaceFileDurabilityError
	if errors.As(err, &uncertain) {
		return &webConversationRequestError{Status: http.StatusServiceUnavailable,
			Detail: "task input publication could not be durably confirmed; the input was not accepted and retained bytes can be retried"}
	}
	return invalidWebConversationInputFile()
}

// Namespace admission is short and grant-guarded. Synchronization happens only
// after that guard has been released, followed by the same authority recheck.
func (s *Server) prepareWebConversationInputDirectory(ctx context.Context, ownerID, projectID, taskRoot string, managed bool) (string, *os.Root, *os.Root, func() error, error) {
	taskRoot, taskDirectory, inputRoot, check, err := s.prepareWebConversationInputNamespace(ownerID, projectID, taskRoot, managed)
	if err != nil {
		return "", nil, nil, nil, err
	}
	failure := func(err error) (string, *os.Root, *os.Root, func() error, error) {
		_ = inputRoot.Close()
		_ = taskDirectory.Close()
		if cause := context.Cause(ctx); cause != nil {
			return "", nil, nil, nil, cause
		}
		return "", nil, nil, nil, webInputPublicationError(err)
	}
	for _, root := range []*os.Root{inputRoot, taskDirectory} {
		if err := syncWorkspaceRootDirectory(ctx, root, s.workspaceDirectorySyncer); err != nil {
			return failure(err)
		}
	}
	if managed {
		if err := s.syncManagedInputAncestors(ctx, taskRoot, taskDirectory); err != nil {
			return failure(err)
		}
	} else if check != nil {
		s.hostGrantKernelMu.Lock()
		err = check()
		s.hostGrantKernelMu.Unlock()
		if err != nil {
			return failure(err)
		}
	}
	current, err := taskDirectory.OpenRoot("inputs")
	if err != nil {
		return failure(err)
	}
	want, wantErr := inputRoot.Stat(".")
	got, gotErr := current.Stat(".")
	_ = current.Close()
	if wantErr != nil || gotErr != nil || !os.SameFile(want, got) {
		return failure(errWorkspaceFileConflict)
	}
	return taskRoot, taskDirectory, inputRoot, check, nil
}

// A managed task root may have been created during workspace selection. Sync
// every ancestor up to its existing storage parent even if it now exists; only
// syncing directories that this call created would miss that earlier namespace.
func (s *Server) syncManagedInputAncestors(ctx context.Context, taskRoot string, taskDirectory *os.Root) error {
	configured, err := canonicalHostDirectory(s.fileRoot)
	if err != nil {
		return &workspaceFileDurabilityError{cause: err}
	}
	basePath := filepath.Dir(configured)
	relative, err := filepath.Rel(basePath, taskRoot)
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errWorkspaceFileConflict
	}
	base, err := os.OpenRoot(basePath)
	if err != nil {
		return &workspaceFileDurabilityError{cause: err}
	}
	defer base.Close()
	want, err := taskDirectory.Stat(".")
	if err != nil {
		return err
	}
	for parent := filepath.Dir(relative); ; parent = filepath.Dir(parent) {
		directory, err := base.Open(parent)
		if err != nil {
			return &workspaceFileDurabilityError{cause: err}
		}
		syncErr := syncWorkspaceDirectory(ctx, directory, s.workspaceDirectorySyncer, false)
		closeErr := directory.Close()
		if syncErr != nil || closeErr != nil {
			return errors.Join(syncErr, closeErr)
		}
		if parent == "." {
			break
		}
	}
	current, err := base.OpenRoot(relative)
	if err != nil {
		return errWorkspaceFileConflict
	}
	defer current.Close()
	got, err := current.Stat(".")
	if err != nil || !os.SameFile(want, got) {
		return errWorkspaceFileConflict
	}
	return nil
}
