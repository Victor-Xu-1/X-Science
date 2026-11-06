package server

import (
	"errors"
	"io/fs"
	"syscall"
)

// Filesystem diagnostics contain no path or arbitrary error/operation text.
// Keep the original error intact for failure classification and recovery.
type sessionRunnerFilesystemFailureFields struct {
	operation, kind string
	errno           uint64
}

func sessionRunnerFilesystemFailure(err error) sessionRunnerFilesystemFailureFields {
	var pathError *fs.PathError
	if !errors.As(err, &pathError) {
		return sessionRunnerFilesystemFailureFields{}
	}
	fields := sessionRunnerFilesystemFailureFields{operation: "other", kind: "other"}
	switch pathError.Op {
	case "stat", "lstat", "open", "read", "write", "close", "readdir", "readlink", "mkdir", "remove", "rename":
		fields.operation = pathError.Op
	}
	var errno syscall.Errno
	if errors.As(pathError.Err, &errno) {
		fields.errno = uint64(errno)
	}
	switch {
	case errors.Is(pathError, fs.ErrPermission):
		fields.kind = "permission"
	case errors.Is(pathError, fs.ErrNotExist):
		fields.kind = "not_found"
	case errors.Is(pathError, syscall.ENAMETOOLONG):
		fields.kind = "name_too_long"
	case errors.Is(pathError, syscall.ENOTDIR):
		fields.kind = "not_directory"
	case errors.Is(pathError, syscall.EIO):
		fields.kind = "io"
	case errors.Is(pathError, syscall.ENOSPC):
		fields.kind = "no_space"
	case errors.Is(pathError, syscall.EROFS):
		fields.kind = "read_only"
	}
	return fields
}
