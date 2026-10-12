//go:build linux

package server

import (
	"os"
	"path/filepath"
	"strconv"
)

func openedFileFinalPath(file *os.File) (string, error) {
	return filepath.EvalSymlinks("/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10))
}
