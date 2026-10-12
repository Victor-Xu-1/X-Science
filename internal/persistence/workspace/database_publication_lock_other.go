//go:build !unix

package workspace

import (
	"errors"
	"path/filepath"
)

func acquireWorkspaceDatabaseHandoffLock(workspaceParent string) (func(), error) {
	_ = filepath.Join(workspaceParent, workspaceDatabaseHandoffLockName)
	return nil, errors.New("workspace database handoff lock is unsupported on this platform")
}
