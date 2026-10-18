//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly || solaris || illumos || aix

package server

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func validateCompatibilitySkillResourceIdentity(file *os.File) error {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		return errors.New("Skill resource must be one regular file")
	}
	return nil
}
