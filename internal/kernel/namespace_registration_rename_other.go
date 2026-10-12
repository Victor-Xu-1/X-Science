//go:build !linux && !darwin && !windows

package kernel

import "errors"

func renameNamespaceRegistrationExclusive(_, _ string) error {
	return errors.New("exclusive environment registration publication is unsupported on this platform")
}
