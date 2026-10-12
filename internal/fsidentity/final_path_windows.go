//go:build windows

package fsidentity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func FinalPath(file *os.File) (string, error) {
	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("cannot verify opened file path")
	}
	finalPath := string(utf16.Decode(buffer[:length]))
	if strings.HasPrefix(finalPath, `\\?\UNC\`) {
		finalPath = `\\` + strings.TrimPrefix(finalPath, `\\?\UNC\`)
	} else {
		finalPath = strings.TrimPrefix(finalPath, `\\?\`)
	}
	return filepath.Clean(finalPath), nil
}
