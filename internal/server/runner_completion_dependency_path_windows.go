//go:build windows

package server

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Keep native path-shape errors local to heuristic candidate probing. Do not
// classify access denial, sharing conflicts or general filesystem I/O as missing.
func sessionRunnerInvalidInferredDependencyPath(err error) bool {
	return errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) ||
		errors.Is(err, windows.ERROR_DIRECTORY) ||
		errors.Is(err, windows.ERROR_INVALID_NAME) ||
		errors.Is(err, windows.ERROR_BAD_PATHNAME)
}
