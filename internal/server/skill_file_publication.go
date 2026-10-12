package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// All publication operations share the caller-owned opened directory. Path
// validation alone cannot protect a write after a parent symlink replacement.
func ensureSkillEditParent(root *os.Root, parent string) error {
	if !filepath.IsLocal(parent) {
		return errors.New("skill edit parent escaped its root")
	}
	current := "."
	for _, segment := range strings.Split(parent, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("skill edit parent contains a non-directory or symlink")
		}
	}
	return nil
}

func writeSkillFileAtomic(root *os.Root, target string, content []byte, exclusive bool) error {
	if !filepath.IsLocal(target) {
		return errors.New("skill write path escaped its root")
	}
	if exclusive {
		file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err = file.Write(content); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		return errors.Join(err, closeErr)
	}
	temporary := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".edit-"+uuid.NewString())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(temporary)
		}
	}()
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if err := root.Rename(temporary, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}
