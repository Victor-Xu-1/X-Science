//go:build windows

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Artifact and upload readers may outlive deletion of their directory entry.
// Delete sharing changes neither read access nor the caller's containment checks.
func openRegularFileReader(path string, expected os.FileInfo) (*os.File, error) {
	name, err := regularFileWindowsPath(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	actual, err := file.Stat()
	if err == nil && (!actual.Mode().IsRegular() || !os.SameFile(expected, actual)) {
		err = errors.New("regular file changed while opening")
	}
	if err != nil {
		_ = file.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return file, nil
}

// CreateFileW needs extended absolute names even when the Go os package accepts
// long local or UNC paths. Existing extended/device namespaces are not prefixed twice.
func regularFileWindowsPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(absolute, `\\?\`) || strings.HasPrefix(absolute, `\\.\`) {
		return absolute, nil
	}
	if strings.HasPrefix(absolute, `\\`) {
		return `\\?\UNC\` + absolute[2:], nil
	}
	return `\\?\` + absolute, nil
}
