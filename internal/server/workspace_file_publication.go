package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"syscall"

	"github.com/google/uuid"
)

var (
	errWorkspaceFileTooLarge = errors.New("workspace file exceeds its byte limit")
	errWorkspaceFileConflict = errors.New("workspace file conflicts with existing content")
)

// stagedWorkspaceFile is private, synced content within an already authorized
// root. All families use the same bounded copy and no-overwrite publication;
// each caller still owns its source authority, naming and capacity policy.
type stagedWorkspaceFile struct {
	root            *os.Root
	name            string
	size            int64
	digest          string
	directorySyncer workspaceDirectorySyncer
}

type workspaceFileCommit func(func() error) error

func stageWorkspaceFile(ctx context.Context, root *os.Root, source io.Reader, maxBytes int64) (*stagedWorkspaceFile, error) {
	return stageWorkspaceFileWithCommit(ctx, root, source, maxBytes, nil)
}

func stageWorkspaceFileWithCommit(ctx context.Context, root *os.Root, source io.Reader, maxBytes int64, commit workspaceFileCommit, syncers ...workspaceDirectorySyncer) (*stagedWorkspaceFile, error) {
	if len(syncers) > 1 {
		return nil, errors.New("workspace publication requires a single directory-sync owner")
	}
	if root == nil || maxBytes < 0 || maxBytes == math.MaxInt64 {
		return nil, errWorkspaceFileTooLarge
	}
	if err := context.Cause(ctx); err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	name := ".x-science-incoming-" + uuid.NewString() + ".tmp"
	var file *os.File
	open := func() error {
		var err error
		file, err = root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		return err
	}
	var err error
	if commit != nil {
		err = commit(open)
	} else {
		err = open()
	}
	if err != nil {
		if file != nil {
			err = errors.Join(err, file.Close(), root.Remove(name))
		}
		return nil, err
	}
	if file == nil {
		return nil, errors.New("workspace file stage was not opened")
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, maxBytes+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	err = errors.Join(copyErr, syncErr, closeErr, ctx.Err(), context.Cause(ctx))
	if size > maxBytes {
		err = errors.Join(err, errWorkspaceFileTooLarge)
	}
	if err != nil {
		return nil, errors.Join(err, root.Remove(name))
	}
	stage := &stagedWorkspaceFile{root: root, name: name, size: size, digest: hex.EncodeToString(hash.Sum(nil))}
	if len(syncers) > 0 {
		stage.directorySyncer = syncers[0]
	}
	return stage, nil
}

func (stage *stagedWorkspaceFile) close() error { return stage.root.Remove(stage.name) }

func (stage *stagedWorkspaceFile) publish(ctx context.Context, destination string) error {
	return stage.publishWithCommit(ctx, destination, nil)
}

// Only namespace publication and the final identity check run inside commit.
// Copying and hashing retain their bounded descriptor reads outside that guard.
func (stage *stagedWorkspaceFile) publishWithCommit(ctx context.Context, destination string, commit workspaceFileCommit) error {
	parent, parentPath, parentInfo, err := workspacePublicationParent(stage.root, destination)
	if err != nil {
		return err
	}
	defer parent.Close()
	checkParent := func() error { return workspacePublicationParentMatches(stage.root, parentPath, parent, parentInfo) }
	apply := func(action func() error) error {
		if err := context.Cause(ctx); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		if commit != nil {
			return commit(action)
		}
		return action()
	}
	// Linking is atomic and never replaces another writer's directory entry.
	// Both paths remain relative to the same held root, including nested upload
	// directories. A symlink replacement cannot redirect the operation outside.
	var linkErr error
	if err := apply(func() error {
		if err := checkParent(); err != nil {
			return err
		}
		linkErr = stage.root.Link(stage.name, destination)
		if errors.Is(linkErr, os.ErrExist) {
			return nil
		}
		return linkErr
	}); err != nil {
		return err
	}
	if linkErr == nil {
		before, err := stage.root.Lstat(stage.name)
		if err != nil {
			return err
		}
		return stage.confirmPublishedDirectory(ctx, destination, parent, checkParent, before, apply)
	}
	file, err := openRegularWorkspaceRootFile(stage.root, destination)
	if err != nil {
		return errWorkspaceFileConflict
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || before.Size() != stage.size {
		return errWorkspaceFileConflict
	}
	size, digest, err := hashOpenedWorkspaceFile(ctx, file, stage.size)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return errors.Join(ctx.Err(), cause)
		}
		return errWorkspaceFileConflict
	}
	if size != stage.size || digest != stage.digest {
		return errWorkspaceFileConflict
	}
	// Same bytes in an existing, different inode must be synced too; the
	// private staged inode's Sync cannot attest another writer's data.
	if err := file.Sync(); err != nil {
		return &workspaceFileDurabilityError{cause: err, published: true}
	}
	if err := apply(func() error {
		current, pathErr := stage.root.Lstat(destination)
		after, statErr := file.Stat()
		if pathErr != nil || statErr != nil || !sameWorkspaceFileSnapshot(before, current) || !sameWorkspaceFileSnapshot(before, after) {
			return errWorkspaceFileConflict
		}
		return nil
	}); err != nil {
		return err
	}
	return stage.confirmPublishedDirectory(ctx, destination, parent, checkParent, before, apply)
}

func (stage *stagedWorkspaceFile) confirmPublishedDirectory(ctx context.Context, destination string, parent *os.File, checkParent func() error, before os.FileInfo, apply workspaceFileCommit) error {
	// Potentially blocking I/O stays outside the grant/namespace commit guard.
	// Failure retains the final file rather than erasing uncertain publication.
	if err := syncWorkspaceDirectory(ctx, parent, stage.directorySyncer, true); err != nil {
		return err
	}
	return apply(func() error {
		if err := checkParent(); err != nil {
			return err
		}
		current, err := stage.root.Lstat(destination)
		if err != nil || !sameWorkspaceFileSnapshot(before, current) {
			return errWorkspaceFileConflict
		}
		return nil
	})
}

func sameWorkspaceFileSnapshot(before, after os.FileInfo) bool {
	return before != nil && after != nil && after.Mode().IsRegular() && os.SameFile(before, after) &&
		before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func hashWorkspaceRootFile(ctx context.Context, root *os.Root, name string, maxBytes int64) (int64, string, error) {
	if root == nil || maxBytes < 0 || maxBytes == math.MaxInt64 {
		return 0, "", errWorkspaceFileTooLarge
	}
	file, err := openRegularWorkspaceRootFile(root, name)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	return hashOpenedWorkspaceFile(ctx, file, maxBytes)
}

func hashOpenedWorkspaceFile(ctx context.Context, file *os.File, maxBytes int64) (int64, string, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return 0, "", errWorkspaceFileConflict
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: file}, maxBytes+1))
	if err != nil {
		return 0, "", err
	}
	if size > maxBytes {
		return 0, "", errWorkspaceFileTooLarge
	}
	return size, hex.EncodeToString(hash.Sum(nil)), ctx.Err()
}

func openRegularWorkspaceRootFile(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errWorkspaceFileConflict
	}
	// Nonblocking open prevents a concurrent regular-file-to-FIFO replacement
	// from hanging before we can inspect the opened descriptor. It has no
	// effect for a regular file (and is ignored by the Windows file opener).
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errWorkspaceFileConflict
	}
	return file, nil
}
