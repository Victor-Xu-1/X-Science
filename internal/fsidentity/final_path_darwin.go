//go:build darwin

package fsidentity

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

func FinalPath(file *os.File) (string, error) {
	var path [unix.MAXPATHLEN]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return "", errno
	}
	resolved := unix.ByteSliceToString(path[:])
	if !filepath.IsAbs(resolved) {
		return "", errors.New("opened handle did not return an absolute path")
	}
	return filepath.Clean(resolved), nil
}
