package server

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"x-science/internal/fsidentity"
)

// Admission, opened-root confinement and final native-handle verification are
// one shared chain. Platform files implement only final-handle path discovery.
func openGrantedRegularFile(path string, grants []hostGrant) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var failure error
	for _, grant := range grants {
		if !hostPathWithin(grant.Path, absolute) {
			continue
		}
		root, err := os.OpenRoot(grant.Path)
		if err != nil {
			failure = err
			continue
		}
		if err := verifyOpenedGrantedDirectory(root, []hostGrant{grant}); err != nil {
			_ = root.Close()
			failure = err
			continue
		}
		file, err := openGrantedRegularFileInRoot(root, grant, absolute)
		closeErr := root.Close()
		if err != nil {
			failure = errors.Join(err, closeErr)
			continue
		}
		if closeErr != nil {
			_ = file.Close()
			failure = closeErr
			continue
		}
		return file, nil
	}
	if failure != nil {
		return nil, failure
	}
	return nil, errors.New("file path is outside granted host directories")
}

// The caller verifies and owns this opened grant root. Keeping it open lets a
// snapshot admission compare the same directory identity at publication time.
func openGrantedRegularFileInRoot(root *os.Root, grant hostGrant, absolute string) (*os.File, error) {
	relative, err := filepath.Rel(grant.Path, absolute)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, errors.New("file path escaped its granted root")
	}
	// NONBLOCK prevents a regular-file-to-FIFO swap from hanging before the
	// descriptor type check. It is ignored by the Windows regular opener.
	file, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	final, finalErr := fsidentity.FinalPath(file)
	if statErr != nil || finalErr != nil || !info.Mode().IsRegular() || !hostPathWithin(grant.Path, final) {
		_ = file.Close()
		return nil, errors.New("opened file is not a regular file inside its granted root")
	}
	return file, nil
}
