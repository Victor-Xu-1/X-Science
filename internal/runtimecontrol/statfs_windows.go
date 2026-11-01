//go:build windows

package runtimecontrol

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func availableBytes(path string) *uint64 {
	directory, err := windowsCapacityDirectory(path)
	if err != nil {
		return nil
	}
	pathPointer, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return nil
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(pathPointer, &available, nil, nil); err != nil {
		return nil
	}
	return &available
}

func windowsCapacityDirectory(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// The metadata handle follows file symlinks and directory junctions before
	// taking a parent, including when their target is on a different volume.
	handle, err := windows.CreateFile(pathPointer, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	buffer := make([]uint16, windows.MAX_PATH)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if length >= uint32(len(buffer)) {
		if length >= windows.MAX_LONG_PATH {
			return "", windows.ERROR_FILENAME_EXCED_RANGE
		}
		buffer = make([]uint16, length+1)
		length, err = windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", windows.ERROR_FILENAME_EXCED_RANGE
	}
	resolved := windows.UTF16ToString(buffer[:length])
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		resolved = filepath.Dir(resolved)
	}
	// GetDiskFreeSpaceExW requires a directory; UNC share roots also require
	// the trailing separator. Missing destinations never borrow an ancestor.
	if !os.IsPathSeparator(resolved[len(resolved)-1]) {
		resolved += string(os.PathSeparator)
	}
	return resolved, nil
}
