//go:build windows

package server

import (
	"os"

	"golang.org/x/sys/windows"
)

func validateCompatibilitySkillResourceIdentity(file *os.File) error {
	return validateAgentWorkspaceWindowsRegular(windows.Handle(file.Fd()), true)
}
