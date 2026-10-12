//go:build !linux && !windows

package server

import (
	"errors"
	"os"
)

func openedFileFinalPath(*os.File) (string, error) {
	return "", errors.New("opened-handle grant verification is unavailable on this platform")
}
