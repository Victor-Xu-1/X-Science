//go:build linux

package datadir

import "golang.org/x/sys/unix"

func namespaceRenameExclusive(source, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}
