package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	skillImportPrivateMarker = ".x-science-import-private"
	skillImportPendingMarker = ".x-science-import-pending"
)

func privateSkillImportDirectory(installRoot string) string {
	return filepath.Join(installRoot, compatibilitySkillDraftDirectory, ".imports")
}

// A private container is on the destination filesystem but is never a catalog
// source. Its explicit marker, not a hidden/backup filename heuristic, binds
// it to the product's unpublished-resource lifecycle.
func createPrivateSkillImportDirectory(parent string) (string, os.FileInfo, error) {
	// Reuse the existing managed draft root: even a stale path captured before
	// cleanup remains structurally outside the executable catalog namespace.
	if err := ensurePrivateDirectoryForImport(filepath.Join(parent, compatibilitySkillDraftDirectory)); err != nil {
		return "", nil, err
	}
	privateRoot := privateSkillImportDirectory(parent)
	if err := ensurePrivateDirectoryForImport(privateRoot); err != nil {
		return "", nil, err
	}
	directory, err := os.MkdirTemp(privateRoot, ".skill-import-")
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", nil, err
	}
	if _, err := createSkillImportMarker(directory, skillImportPrivateMarker); err != nil {
		_ = os.RemoveAll(directory)
		return "", nil, err
	}
	return directory, info, nil
}

func createSkillImportMarker(root, name string) (os.FileInfo, error) {
	file, err := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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

func skillImportMarkerPresent(root, name string) bool {
	_, err := os.Lstat(filepath.Join(root, name))
	// Inaccessible/corrupt control state is not proof of publication.
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func managedSkillImportPath(path, fileRoot string) bool {
	if strings.TrimSpace(fileRoot) == "" || !filepath.IsAbs(fileRoot) {
		return false
	}
	root := personalSkillDirectory(fileRoot)
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return false
	}
	first := strings.Split(relative, string(filepath.Separator))[0]
	directory := filepath.Join(root, first)
	return skillImportMarkerPresent(directory, skillImportPrivateMarker) ||
		skillImportMarkerPresent(directory, skillImportPendingMarker)
}

func unpublishedManagedSkillPath(path, fileRoot string) bool {
	return managedSkillDraftPath(path, fileRoot) || managedSkillImportPath(path, fileRoot)
}

func validateSkillImportResourceNames(files []string) error {
	for _, file := range files {
		name := filepath.Base(file)
		if strings.EqualFold(name, skillImportPrivateMarker) || strings.EqualFold(name, skillImportPendingMarker) {
			return fmt.Errorf("skill bundle contains reserved import control resource %q", file)
		}
	}
	return nil
}

func (entry *skillDirectoryPublication) confirmImportVisibility() error {
	directory, err := os.Lstat(entry.destination)
	if err != nil || !os.SameFile(directory, entry.published) {
		return errors.New("publication destination identity changed before visibility confirmation")
	}
	marker := filepath.Join(entry.destination, skillImportPendingMarker)
	info, err := os.Lstat(marker)
	if err != nil || entry.pending == nil || !os.SameFile(info, entry.pending) {
		return errors.New("publication pending marker identity changed before visibility confirmation")
	}
	return os.Remove(marker)
}

func (entry *skillDirectoryPublication) cleanupBackupContainer() {
	info, err := os.Lstat(entry.backupRoot)
	if err != nil || entry.backupOwner == nil || !os.SameFile(info, entry.backupOwner) {
		log.Printf("Skill publication retained a backup container whose ownership changed")
		return
	}
	children, err := os.ReadDir(entry.backupRoot)
	if err != nil {
		return
	}
	for _, child := range children {
		if child.Name() != skillImportPrivateMarker && child.Name() != "previous" {
			log.Printf("Skill publication retained unexpected backup-container resources")
			return
		}
		if child.Name() == "previous" {
			previous, err := os.Lstat(entry.backup)
			if err != nil || !os.SameFile(previous, entry.previous) {
				return
			}
		}
	}
	if err := os.RemoveAll(entry.backupRoot); err != nil {
		log.Printf("Skill publication retained a private backup after cleanup failure")
	}
}
