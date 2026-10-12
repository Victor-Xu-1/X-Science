//go:build !linux && !windows && !darwin

package datadir

import "errors"

func namespaceRenameExclusive(_, _ string) error {
	return errors.New("namespace upgrade requires atomic no-replace rename support")
}
