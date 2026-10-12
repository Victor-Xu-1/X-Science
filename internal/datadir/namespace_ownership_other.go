//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package datadir

import (
	"errors"
	"os"
)

func namespaceOwned(_ string, _ os.FileInfo, _ bool) error {
	return errors.New("namespace upgrade requires operating-system ownership verification")
}
