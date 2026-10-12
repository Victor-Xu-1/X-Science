//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package datadir

import (
	"errors"
	"os"
)

func openNamespaceLock(string) (*os.File, error) {
	return nil, errors.New("namespace upgrade requires an operating-system advisory lock")
}

func acquireNamespaceLock(*os.File) error {
	return errors.New("namespace upgrade requires an operating-system advisory lock")
}

func releaseNamespaceLock(file *os.File) { file.Close() }
