package compute

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	managedEndpointOutputCursorVersion  = 1
	managedEndpointOutputCursorMaxBytes = 256 << 10
)

// A pending cursor is durable before any segment is appended or truncated.
// Lengths alone cannot detect an interrupted full-segment overwrite. Only a
// clean cursor published after segment Sync can authorize further rotation.
type managedEndpointOutputCursor struct {
	Version   int                         `json:"version"`
	Directory string                      `json:"directory"`
	Policy    ManagedEndpointOutputPolicy `json:"policy"`
	Segment   int                         `json:"segment"`
	Used      int64                       `json:"used"`
	Total     uint64                      `json:"total"`
	Dropped   uint64                      `json:"dropped"`
	Retained  []int64                     `json:"retained"`
	Sequence  uint64                      `json:"sequence"`
	Pending   bool                        `json:"pending"`
}

type managedEndpointOutputHistoryError struct{ reason string }

func (err *managedEndpointOutputHistoryError) Error() string {
	return "managed endpoint retained output continuity is unknown: " + err.reason
}

func (*managedEndpointOutputHistoryError) Unwrap() error { return ErrManagedEndpointOutcomeUnknown }

func managedEndpointUnknownOutputHistory(reason string) error {
	return &managedEndpointOutputHistoryError{reason: reason}
}

func validManagedEndpointOutputPolicy(policy ManagedEndpointOutputPolicy) bool {
	return policy.SegmentBytes >= 4096 && policy.SegmentBytes <= 8<<20 && policy.Segments >= 1 && policy.Segments <= 8
}

func (output *managedEndpointOutput) persistCursor(pending bool) error {
	cursor := managedEndpointOutputCursor{
		Version: managedEndpointOutputCursorVersion, Directory: output.dir, Policy: output.policy,
		Segment: output.segment, Used: output.used, Total: output.total, Dropped: output.dropped,
		Retained: append([]int64(nil), output.retained...), Sequence: output.sequence, Pending: pending,
	}
	return writeManagedEndpointPrivateJSON(filepath.Join(output.dir, "cursor.json"), cursor)
}

func readManagedEndpointOutputCursor(dir string, policy ManagedEndpointOutputPolicy) (managedEndpointOutputCursor, error) {
	var cursor managedEndpointOutputCursor
	path := filepath.Join(dir, "cursor.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return cursor, managedEndpointUnknownOutputHistory("cursor is missing")
	}
	if err != nil {
		return cursor, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return cursor, errors.New("managed endpoint output cursor is not a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return cursor, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return cursor, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return cursor, errors.New("managed endpoint output cursor identity changed")
	}
	if opened.Size() > managedEndpointOutputCursorMaxBytes {
		return cursor, managedEndpointUnknownOutputHistory("cursor exceeds its bounded format")
	}
	decoder := json.NewDecoder(io.LimitReader(file, managedEndpointOutputCursorMaxBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return cursor, managedEndpointOutputCursorDecodeError(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return cursor, managedEndpointUnknownOutputHistory("cursor contains trailing data")
		}
		return cursor, managedEndpointOutputCursorDecodeError(err)
	}
	if cursor.Version != managedEndpointOutputCursorVersion || cursor.Directory != dir || cursor.Policy != policy || cursor.Pending || cursor.Segment < 0 || cursor.Segment >= policy.Segments || cursor.Used < 0 || cursor.Used > policy.SegmentBytes || len(cursor.Retained) != policy.Segments {
		return cursor, managedEndpointUnknownOutputHistory("cursor has no matching clean ring state")
	}
	var retained uint64
	for _, size := range cursor.Retained {
		if size < 0 || size > policy.SegmentBytes {
			return cursor, managedEndpointUnknownOutputHistory("cursor retained lengths are invalid")
		}
		retained += uint64(size)
	}
	if cursor.Retained[cursor.Segment] != cursor.Used || cursor.Dropped > cursor.Total || retained != cursor.Total-cursor.Dropped || cursor.Sequence == 0 && cursor.Total != 0 {
		return cursor, managedEndpointUnknownOutputHistory("cursor counters contradict retained lengths")
	}
	return cursor, nil
}

func managedEndpointOutputCursorDecodeError(err error) error {
	// Filesystem access failures retain their hard error class. Only malformed
	// private history may use the worker's nonpersistent drain mode.
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return err
	}
	return managedEndpointUnknownOutputHistory(fmt.Sprintf("cursor could not be decoded (%T)", err))
}

func (output *managedEndpointOutput) continuityWarning() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	if !output.unverified {
		return ""
	}
	return "Earlier output continuity and cumulative counters are unknown. Current-reader output is drained but not persisted; its sequence and dropped-byte counts cover only this reader. Retained historical files are unchanged."
}
