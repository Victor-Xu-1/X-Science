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

func managedSkillImportPath(path, fileRoot string) bool {
	if strings.TrimSpace(fileRoot) == "" || !filepath.IsAbs(fileRoot) {
		return false
	}
	root := personalSkillDirectory(fileRoot)
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return false
	}
	directory := root
	if filepath.Dir(relative) != "." {
		first := strings.Split(relative, string(filepath.Separator))[0]
		directory = filepath.Join(root, first)
	}
	filesystem, err := openSkillImportFilesystem(fileRoot, false)
	if err != nil {
		return true
	}
	defer filesystem.close()
	return filesystem.markerPresent(directory, skillImportPrivateMarker) ||
		filesystem.markerPresent(directory, skillImportPendingMarker)
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
	if err := entry.filesystem.verifyBinding(); err != nil {
		return err
	}
	directory, err := entry.filesystem.lstat(entry.destination)
	if err != nil || !os.SameFile(directory, entry.published) {
		return errors.New("publication destination identity changed before visibility confirmation")
	}
	marker := filepath.Join(entry.destination, skillImportPendingMarker)
	info, err := entry.filesystem.lstat(marker)
	if err != nil || entry.pending == nil || !os.SameFile(info, entry.pending) {
		return errors.New("publication pending marker identity changed before visibility confirmation")
	}
	return entry.filesystem.remove(marker, false)
}

func (entry *skillDirectoryPublication) cleanupBackupContainer() {
	info, err := entry.filesystem.lstat(entry.backupRoot)
	if err != nil || entry.backupOwner == nil || !os.SameFile(info, entry.backupOwner) {
		log.Printf("Skill publication retained a backup container whose ownership changed")
		return
	}
	children, err := entry.filesystem.readDir(entry.backupRoot)
	if err != nil {
		return
	}
	for _, child := range children {
		if child.Name() != skillImportPrivateMarker && child.Name() != "previous" {
			log.Printf("Skill publication retained unexpected backup-container resources")
			return
		}
		if child.Name() == "previous" {
			previous, err := entry.filesystem.lstat(entry.backup)
			if err != nil || !os.SameFile(previous, entry.previous) {
				return
			}
		}
	}
	if err := entry.filesystem.remove(entry.backupRoot, true); err != nil {
		log.Printf("Skill publication retained a private backup after cleanup failure")
	}
}
