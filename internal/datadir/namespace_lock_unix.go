//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package datadir

import (
	"os"

	"golang.org/x/sys/unix"
)

func openNamespaceLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func acquireNamespaceLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func releaseNamespaceLock(file *os.File) {
	unix.Flock(int(file.Fd()), unix.LOCK_UN)
	file.Close()
}
