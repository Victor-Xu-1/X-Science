package server

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestRunnerFilesystemFailureDiagnosticsAreTypedAndPathFree(t *testing.T) {
	// Windows aliases ENOTDIR to ERROR_PATH_NOT_FOUND. Preserve the standard
	// library's not-found classification where no distinct directory errno exists.
	notDirectoryKind := "not_directory"
	if errors.Is(syscall.ENOTDIR, os.ErrNotExist) {
		notDirectoryKind = "not_found"
	}
	for _, test := range []struct {
		name, op, kind string
		cause          error
		errno          uint64
	}{
		{"permission", "open", "permission", syscall.EACCES, uint64(syscall.EACCES)},
		{"missing", "stat", "not_found", syscall.ENOENT, uint64(syscall.ENOENT)},
		{"long name", "stat", "name_too_long", syscall.ENAMETOOLONG, uint64(syscall.ENAMETOOLONG)},
		{"not directory", "stat", notDirectoryKind, syscall.ENOTDIR, uint64(syscall.ENOTDIR)},
		{"storage io", "read", "io", syscall.EIO, uint64(syscall.EIO)},
		{"untrusted operation and error", "PRIVATE_OPERATION_SENTINEL", "other", errors.New("PRIVATE_ERROR_SENTINEL"), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := &os.PathError{Op: test.op, Path: "PRIVATE_PATH_SENTINEL", Err: test.cause}
			wrapped := &sessionRunnerCompletionFailure{stage: completionArtifactEvidence, err: fmt.Errorf("context: %w", cause)}
			fields := sessionRunnerFilesystemFailure(wrapped)
			if fields.kind != test.kind || fields.errno != test.errno || !errors.Is(wrapped, cause) {
				t.Fatalf("safe fields=%#v original classification preserved=%t", fields, errors.Is(wrapped, cause))
			}
			if strings.Contains(fmt.Sprintf("%+v", fields), "PRIVATE_") {
				t.Fatal("filesystem diagnostics copied arbitrary operation/path/error text")
			}
		})
	}
	if got := sessionRunnerFilesystemFailure(errors.New("PRIVATE_NON_FILESYSTEM")); got != (sessionRunnerFilesystemFailureFields{}) {
		t.Fatalf("non-filesystem error invented metadata: %#v", got)
	}
}
