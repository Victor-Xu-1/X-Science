//go:build darwin

package kernel

import "golang.org/x/sys/unix"

func renameNamespaceRegistrationExclusive(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
