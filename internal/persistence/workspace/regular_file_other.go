//go:build !windows

package workspace

import "os"

// Keep the existing Unix open behavior after the common regular-file check.
func openRegularFileReader(path string, _ os.FileInfo) (*os.File, error) {
	return os.Open(path)
}
