//go:build !windows

package productstate

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
	"time"
)

func checkOwner(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Gid != uint32(os.Getegid()) || info.Mode().Perm()&0o022 != 0 || (info.Mode().IsRegular() && stat.Nlink != 1) {
		return errors.New("private state has foreign ownership or unsafe permissions")
	}
	return nil
}
func ownershipFacts(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("private state ownership is unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Uid, stat.Gid), nil
}
func openOwnedFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil {
		err = checkOwner(path, info)
	}
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("private state file is unconfirmed")
	}
	return file, nil
}
func lockNamespace(ctx context.Context, path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil {
		err = checkOwner(path, info)
	}
	if err == nil {
		for {
			err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
			if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func unlockNamespace(file *os.File) { unix.Flock(int(file.Fd()), unix.LOCK_UN); file.Close() }
func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
func createOwnedFile(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
}
