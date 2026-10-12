//go:build unix

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func acquireWorkspaceDatabaseHandoffLock(workspaceParent string) (func(), error) {
	lockPath := filepath.Join(workspaceParent, workspaceDatabaseHandoffLockName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, errors.New("workspace database handoff is already active or its lock is unsafe")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
