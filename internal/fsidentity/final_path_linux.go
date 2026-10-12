//go:build linux

package fsidentity

import (
	"os"
	"path/filepath"
	"strconv"
)

func FinalPath(file *os.File) (string, error) {
	return filepath.EvalSymlinks("/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10))
}
