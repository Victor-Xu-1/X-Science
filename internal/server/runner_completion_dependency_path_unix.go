//go:build !windows

package server

import (
	"errors"
	"syscall"
)

// A heuristic source string that cannot name a file is not a dependency.
// This classification applies only to the inferred candidate's os.Stat probe;
// permission, I/O and produced-artifact failures retain their original errors.
func sessionRunnerInvalidInferredDependencyPath(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.ENOTDIR)
}
