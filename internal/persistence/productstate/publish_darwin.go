//go:build darwin

package productstate

import "golang.org/x/sys/unix"

func publishMissing(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
