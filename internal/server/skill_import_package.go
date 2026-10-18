package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func listImportedSkillFiles(root *os.Root) ([]string, error) {
	files := make([]string, 0, 8)
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list imported skill files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func webSkillDirectoryHash(root *os.Root) (string, error) {
	hasher := sha256.New()
	files := 0
	var total int64
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("skill directory contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("skill directory contains a non-regular file")
		}
		files++
		if info.Size() > maxSkillArchiveBytes-total || files > maxSkillArchiveFiles {
			return errors.New("skill directory exceeds verification limits")
		}
		total += info.Size()
		file, err := root.OpenFile(filepath.FromSlash(path), os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(opened, info) {
			_ = file.Close()
			return errors.New("skill file changed while it was opened")
		}
		_, _ = hasher.Write([]byte(path))
		_, _ = hasher.Write([]byte{0})
		written, copyErr := io.Copy(hasher, io.LimitReader(file, info.Size()+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if written != info.Size() {
			return errors.New("skill file changed while it was hashed")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func extractSkillArchive(root *os.Root, raw []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("open skill archive: %w", err)
	}
	if len(archive.File) > maxSkillArchiveFiles {
		return fmt.Errorf("skill bundle exceeds %d archive entries", maxSkillArchiveFiles)
	}
	seen := map[string]bool{}
	var extractedBytes int64
	for _, entry := range archive.File {
		name := strings.TrimSpace(entry.Name)
		if name == "" || strings.Contains(name, "\\") {
			return errors.New("skill archive contains an invalid path")
		}
		cleaned := pathpkg.Clean(name)
		if cleaned == "." || pathpkg.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("skill archive path %q escapes the bundle", name)
		}
		key := strings.ToLower(cleaned)
		if seen[key] {
			return fmt.Errorf("skill archive contains duplicate path %q", cleaned)
		}
		seen[key] = true
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !mode.IsRegular()) {
			return fmt.Errorf("skill archive path %q is not a regular file or directory", cleaned)
		}
		target := filepath.FromSlash(cleaned)
		if !filepath.IsLocal(target) {
			return fmt.Errorf("skill archive path %q escapes the bundle", name)
		}
		if entry.FileInfo().IsDir() {
			if err := ensureSkillEditParent(root, target); err != nil {
				return fmt.Errorf("create skill archive directory: %w", err)
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxSkillArchiveBytes) || extractedBytes+int64(entry.UncompressedSize64) > maxSkillArchiveBytes {
			return fmt.Errorf("skill bundle exceeds %d extracted bytes", maxSkillArchiveBytes)
		}
		if err := ensureSkillEditParent(root, filepath.Dir(target)); err != nil {
			return fmt.Errorf("create skill archive parent: %w", err)
		}
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open skill archive entry: %w", err)
		}
		destination, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = source.Close()
			return fmt.Errorf("create skill archive entry: %w", err)
		}
		written, copyErr := io.Copy(destination, io.LimitReader(source, maxSkillArchiveBytes-extractedBytes+1))
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil {
			return fmt.Errorf("extract skill archive entry: %w", copyErr)
		}
		if closeDestinationErr != nil || closeSourceErr != nil {
			return errors.New("close extracted skill archive entry")
		}
		extractedBytes += written
		if extractedBytes > maxSkillArchiveBytes || written != int64(entry.UncompressedSize64) {
			return errors.New("skill archive entry size did not match its authenticated bounds")
		}
	}
	return nil
}
