package server

import (
	"context"
	"os"
	"path/filepath"
)

type workspaceDirectorySyncer func(*os.File) error

// A linked file is retained on uncertainty. Callers must not admit its path as
// a durable input until the filesystem confirms the directory publication.
type workspaceFileDurabilityError struct {
	cause     error
	published bool
}

func (err *workspaceFileDurabilityError) Error() string {
	return "workspace publication durability could not be confirmed"
}

func (err *workspaceFileDurabilityError) Unwrap() error { return err.cause }

func syncWorkspaceDirectory(ctx context.Context, directory *os.File, syncer workspaceDirectorySyncer, published bool) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	info, err := directory.Stat()
	if err == nil && !info.IsDir() {
		err = errWorkspaceFileConflict
	}
	if err == nil {
		if syncer != nil {
			err = syncer(directory)
		} else {
			err = directory.Sync()
		}
	}
	if err != nil {
		return &workspaceFileDurabilityError{cause: err, published: published}
	}
	return context.Cause(ctx)
}

func syncWorkspaceRootDirectory(ctx context.Context, root *os.Root, syncer workspaceDirectorySyncer) error {
	directory, err := root.Open(".")
	if err != nil {
		return &workspaceFileDurabilityError{cause: err}
	}
	defer directory.Close()
	return syncWorkspaceDirectory(ctx, directory, syncer, false)
}

func workspacePublicationParent(root *os.Root, destination string) (*os.File, string, os.FileInfo, error) {
	parent := filepath.Dir(destination)
	directory, err := root.Open(parent)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		_ = directory.Close()
		return nil, "", nil, errWorkspaceFileConflict
	}
	return directory, parent, info, nil
}

func workspacePublicationParentMatches(root *os.Root, path string, opened *os.File, before os.FileInfo) error {
	current, pathErr := root.Stat(path)
	after, statErr := opened.Stat()
	if pathErr != nil || statErr != nil || !current.IsDir() || !after.IsDir() ||
		!os.SameFile(before, current) || !os.SameFile(before, after) {
		return errWorkspaceFileConflict
	}
	return nil
}
