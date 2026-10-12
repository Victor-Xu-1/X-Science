//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package datadir

import (
	"errors"
	"os"
	"syscall"
)

func namespaceOwned(_ string, info os.FileInfo, private bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("namespace source belongs to a different operating-system owner")
	}
	if info.Mode().IsRegular() && stat.Nlink != 1 {
		return errors.New("namespace source has a hard-link alias")
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return errors.New("namespace control source must be private to its owner")
	}
	return nil
}
