//go:build darwin

package datadir

import "golang.org/x/sys/unix"

func namespaceRenameExclusive(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
