package server

import (
	"errors"
	"os"
)

// Validate the actual opened directory, not the path used to select it. This
// shares native final-handle authority with regular-file reads on each platform.
func verifyOpenedGrantedDirectory(root *os.Root, grants []hostGrant) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return errors.New("opened grant root is not a directory")
	}
	final, err := openedFileFinalPath(file)
	if err != nil {
		return err
	}
	for _, grant := range grants {
		if hostPathWithin(grant.Path, final) {
			return nil
		}
	}
	return errors.New("opened directory is outside granted host directories")
}
