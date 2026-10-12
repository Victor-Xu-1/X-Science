package compute

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const LocalDownloadMaxBytes int64 = 2 << 30
const LocalImportMaxBytes int64 = 50 << 20

type LocalDownload struct {
	File     *os.File
	Filename string
	Size     int64
	MTime    int64
}

func OpenLocalDownload(path, confine, workspaceRoot string) (LocalDownload, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return LocalDownload{}, errors.New("path must be absolute and contain no control characters")
	}
	if confine != "workspaces" {
		return LocalDownload{}, errors.New("confine must be workspaces")
	}
	rootPath := strings.TrimSpace(workspaceRoot)
	if !filepath.IsAbs(rootPath) {
		return LocalDownload{}, errors.New("workspace root is unavailable")
	}
	relative, err := filepath.Rel(filepath.Clean(rootPath), filepath.Clean(path))
	if err != nil || !filepath.IsLocal(relative) {
		return LocalDownload{}, errors.New("path is outside the workspace root")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return LocalDownload{}, err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return LocalDownload{}, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return LocalDownload{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		file.Close()
		return LocalDownload{}, errors.New("path is not a non-empty regular file")
	}
	if info.Size() > LocalDownloadMaxBytes {
		file.Close()
		return LocalDownload{}, errors.New("file exceeds the 2 GiB download limit")
	}
	return LocalDownload{File: file, Filename: filepath.Base(path), Size: info.Size(), MTime: info.ModTime().UnixMilli()}, nil
}
