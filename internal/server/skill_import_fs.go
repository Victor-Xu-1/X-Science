package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// The only authority used for import publication is the configured FileRoot.
// String paths are presentation coordinates; every IO operation is relative to
// the same opened skills directory, never a root reconstructed from a payload.
type skillImportFilesystem struct {
	parent    *os.Root
	root      *os.Root
	path      string
	identity  os.FileInfo
	closeOnce sync.Once
}

func openSkillImportFilesystem(fileRoot string, create bool) (*skillImportFilesystem, error) {
	if !filepath.IsAbs(fileRoot) {
		return nil, errors.New("absolute Skill FileRoot is required")
	}
	parent, err := os.OpenRoot(fileRoot)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*skillImportFilesystem, error) {
		_ = parent.Close()
		return nil, err
	}
	info, err := parent.Lstat("skills")
	if errors.Is(err, os.ErrNotExist) && create {
		if err := parent.Mkdir("skills", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fail(err)
		}
		info, err = parent.Lstat("skills")
	}
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail(errors.New("Skill root must be a regular directory"))
	}
	root, err := parent.OpenRoot("skills")
	if err != nil {
		return fail(err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return fail(errors.New("Skill root changed while its capability was opened"))
	}
	if create && opened.Mode().Perm()&0o077 != 0 {
		if err := chmodSkillImportDirectory(root, "."); err != nil {
			_ = root.Close()
			return fail(err)
		}
	}
	return &skillImportFilesystem{parent: parent, root: root, path: filepath.Join(fileRoot, "skills"), identity: opened}, nil
}

func (filesystem *skillImportFilesystem) close() {
	filesystem.closeOnce.Do(func() {
		_ = filesystem.root.Close()
		_ = filesystem.parent.Close()
	})
}

func (filesystem *skillImportFilesystem) verifyBinding() error {
	current, err := filesystem.parent.Lstat("skills")
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, filesystem.identity) {
		return errors.New("Skill root no longer matches its opened publication capability")
	}
	return nil
}

func (filesystem *skillImportFilesystem) relative(path string) (string, error) {
	relative, err := filepath.Rel(filesystem.path, path)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("Skill import path escaped its opened root")
	}
	return relative, nil
}

func (filesystem *skillImportFilesystem) lstat(path string) (os.FileInfo, error) {
	relative, err := filesystem.relative(path)
	if err != nil {
		return nil, err
	}
	return filesystem.root.Lstat(relative)
}

func (filesystem *skillImportFilesystem) rename(source, destination string) error {
	from, err := filesystem.relative(source)
	if err != nil || from == "." {
		return errors.New("invalid Skill publication source")
	}
	to, err := filesystem.relative(destination)
	if err != nil || to == "." {
		return errors.New("invalid Skill publication destination")
	}
	return filesystem.root.Rename(from, to)
}

func (filesystem *skillImportFilesystem) remove(path string, recursive bool) error {
	relative, err := filesystem.relative(path)
	if err != nil || relative == "." {
		return errors.New("invalid Skill cleanup path")
	}
	if recursive {
		return filesystem.root.RemoveAll(relative)
	}
	return filesystem.root.Remove(relative)
}

func (filesystem *skillImportFilesystem) removeOwnedDirectory(path string, identity os.FileInfo) error {
	current, err := filesystem.lstat(path)
	if err != nil {
		return err
	}
	if identity == nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, identity) {
		return errors.New("Skill cleanup directory no longer matches its captured owner")
	}
	return filesystem.remove(path, true)
}

func (filesystem *skillImportFilesystem) readDir(path string) ([]os.DirEntry, error) {
	relative, err := filesystem.relative(path)
	if err != nil {
		return nil, err
	}
	directory, err := filesystem.root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}

func (filesystem *skillImportFilesystem) openDirectory(path string) (*os.Root, error) {
	relative, err := filesystem.relative(path)
	if err != nil {
		return nil, err
	}
	return filesystem.root.OpenRoot(relative)
}

func (filesystem *skillImportFilesystem) createPrivateDirectory() (string, os.FileInfo, error) {
	relative := filepath.Join(compatibilitySkillDraftDirectory, ".imports")
	if err := ensureSkillEditParent(filesystem.root, relative); err != nil {
		return "", nil, err
	}
	for _, parent := range []string{compatibilitySkillDraftDirectory, relative} {
		if err := chmodSkillImportDirectory(filesystem.root, parent); err != nil {
			return "", nil, err
		}
	}
	relative = filepath.Join(relative, ".skill-import-"+uuid.NewString())
	if err := filesystem.root.Mkdir(relative, 0o700); err != nil {
		return "", nil, err
	}
	directory := filepath.Join(filesystem.path, relative)
	info, err := filesystem.root.Lstat(relative)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("Skill staging path is no longer a regular directory")
	}
	if _, err := filesystem.createMarker(directory, skillImportPrivateMarker); err != nil {
		return "", nil, errors.Join(err, filesystem.removeOwnedDirectory(directory, info))
	}
	return directory, info, nil
}

func chmodSkillImportDirectory(root *os.Root, relative string) error {
	directory, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Chmod(0o700)
}

func (filesystem *skillImportFilesystem) createMarker(directory, name string) (os.FileInfo, error) {
	if name != skillImportPrivateMarker && name != skillImportPendingMarker {
		return nil, errors.New("unknown Skill import control marker")
	}
	relative, err := filesystem.relative(filepath.Join(directory, name))
	if err != nil {
		return nil, err
	}
	file, err := filesystem.root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.WriteString("x-science.skill-import-visibility.v1\n")
	info, statErr := file.Stat()
	if err := errors.Join(writeErr, statErr, file.Close()); err != nil {
		return nil, err
	}
	return info, nil
}

func (filesystem *skillImportFilesystem) markerPresent(directory, name string) bool {
	_, err := filesystem.lstat(filepath.Join(directory, name))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func (filesystem *skillImportFilesystem) destination(name string) (string, bool, error) {
	if !importedSkillNamePattern.MatchString(name) || name == "." || name == ".." {
		return "", false, errors.New("invalid Skill destination name")
	}
	entries, err := filesystem.readDir(filesystem.path)
	if err != nil {
		return "", false, fmt.Errorf("list installed Skills: %w", err)
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) {
			return filepath.Join(filesystem.path, entry.Name()), true, nil
		}
	}
	return filepath.Join(filesystem.path, name), false, nil
}
