//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !solaris && !illumos && !aix && !windows

package server

import (
	"errors"
	"os"
)

func validateCompatibilitySkillResourceIdentity(file *os.File) error {
	return errors.New("Skill resource validation is unsupported on this platform")
}
