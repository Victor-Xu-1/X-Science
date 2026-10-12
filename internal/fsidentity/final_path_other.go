//go:build !linux && !windows && !darwin

package fsidentity

import (
	"errors"
	"os"
)

func FinalPath(*os.File) (string, error) {
	return "", errors.New("opened-handle grant verification is unavailable on this platform")
}
