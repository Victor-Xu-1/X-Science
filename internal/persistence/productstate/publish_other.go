//go:build !linux && !windows && !darwin

package productstate

import "errors"

func publishMissing(string, string) error {
	return errors.New("atomic private namespace upgrade is unsupported on this platform")
}
