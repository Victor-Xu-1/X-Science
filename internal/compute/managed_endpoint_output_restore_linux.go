//go:build linux

package compute

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func reopenManagedEndpointOutput(dir string, policy ManagedEndpointOutputPolicy) (*managedEndpointOutput, error) {
	if !filepath.IsAbs(dir) || !validManagedEndpointOutputPolicy(policy) {
		return nil, errors.New("invalid managed endpoint output policy")
	}
	directory, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 || directory.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("managed endpoint output directory is not private")
	}
	// Validate filesystem authority before classifying any missing/malformed
	// cursor as historical uncertainty. Permission and symlink errors may not
	// be downgraded by worker bootstrap.
	output := &managedEndpointOutput{dir: dir, policy: policy}
	segments := make([]os.FileInfo, policy.Segments)
	for index := 0; index < policy.Segments; index++ {
		info, err := os.Lstat(output.segmentPath(index))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("managed endpoint retained output is not a private regular file")
		}
		segments[index] = info
	}
	cursor, err := readManagedEndpointOutputCursor(dir, policy)
	if err != nil {
		return nil, err
	}
	for index, info := range segments {
		if info == nil {
			if cursor.Retained[index] != 0 || index == cursor.Segment {
				return nil, managedEndpointUnknownOutputHistory("cursor segment is missing")
			}
		} else if info.Size() != cursor.Retained[index] {
			return nil, managedEndpointUnknownOutputHistory("cursor segment length changed")
		}
	}
	output.retained = cursor.Retained
	output.segment, output.used = cursor.Segment, cursor.Used
	output.total, output.dropped, output.sequence = cursor.Total, cursor.Dropped, cursor.Sequence
	file, err := os.OpenFile(output.segmentPath(cursor.Segment), os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(segments[cursor.Segment], opened) || opened.Size() != cursor.Used {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("managed endpoint active output segment identity changed")
	}
	if _, err := file.Seek(cursor.Used, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	output.file = file
	return output, nil
}

func managedEndpointOutputForWorker(dir string, policy ManagedEndpointOutputPolicy) (*managedEndpointOutput, bool, error) {
	output, err := newManagedEndpointOutput(dir, policy)
	if !errors.Is(err, os.ErrExist) {
		return output, false, err
	}
	output, err = reopenManagedEndpointOutput(dir, policy)
	var historyError *managedEndpointOutputHistoryError
	if errors.As(err, &historyError) {
		// The strict reader has already verified this private directory and
		// regular segment permissions. Its historical cursor is not physical
		// owner authority: do not let uncertain logs prevent an exact Stop.
		output = &managedEndpointOutput{dir: dir, policy: policy, retained: make([]int64, policy.Segments), unverified: true}
		return output, true, nil
	}
	return output, true, err
}
