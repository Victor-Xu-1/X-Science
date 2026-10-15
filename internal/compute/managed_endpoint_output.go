package compute

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// The worker, not an HTTP request, owns this bounded output sink. A full ring
// drops retained history, never closes a live service's write descriptor.
type managedEndpointOutput struct {
	mu         sync.Mutex
	dir        string
	policy     ManagedEndpointOutputPolicy
	file       *os.File
	segment    int
	used       int64
	total      uint64
	dropped    uint64
	retained   []int64
	sequence   uint64
	closed     bool
	writeError error
	prefix     cappedBuffer
}

func newManagedEndpointOutput(dir string, policy ManagedEndpointOutputPolicy) (*managedEndpointOutput, error) {
	if !filepath.IsAbs(dir) || policy.SegmentBytes < 4096 || policy.SegmentBytes > 8<<20 || policy.Segments < 1 || policy.Segments > 8 {
		return nil, errors.New("invalid managed endpoint output policy")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	output := &managedEndpointOutput{dir: dir, policy: policy, retained: make([]int64, policy.Segments)}
	file, err := os.OpenFile(output.segmentPath(0), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	output.file = file
	return output, nil
}

func (output *managedEndpointOutput) segmentPath(index int) string {
	return filepath.Join(output.dir, fmt.Sprintf("output-%d", index))
}

func (output *managedEndpointOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	original := len(value)
	if output.closed {
		return 0, os.ErrClosed
	}
	output.sequence++
	output.total += uint64(original)
	_, _ = output.prefix.Write(value)
	if output.writeError != nil {
		output.dropped += uint64(original)
		return original, nil
	}
	for len(value) > 0 {
		if output.used == output.policy.SegmentBytes {
			if err := output.file.Sync(); err != nil {
				output.writeError = err
				output.dropped += uint64(len(value))
				return original, nil
			}
			if err := output.file.Close(); err != nil {
				output.writeError = err
				output.dropped += uint64(len(value))
				return original, nil
			}
			output.segment = (output.segment + 1) % output.policy.Segments
			path := output.segmentPath(output.segment)
			if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
				output.writeError = errors.New("managed endpoint output segment is not a regular file")
				output.dropped += uint64(len(value))
				return original, nil
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				output.writeError = err
				output.dropped += uint64(len(value))
				return original, nil
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				output.writeError = err
				output.dropped += uint64(len(value))
				return original, nil
			}
			output.dropped += uint64(output.retained[output.segment])
			output.retained[output.segment] = 0
			output.file, output.used = file, 0
		}
		n := min(len(value), int(output.policy.SegmentBytes-output.used))
		written, err := output.file.Write(value[:n])
		output.used += int64(written)
		output.retained[output.segment] += int64(written)
		value = value[written:]
		if err != nil {
			output.writeError = err
			output.dropped += uint64(len(value))
			return original, nil
		}
		if written != n {
			output.writeError = io.ErrShortWrite
			output.dropped += uint64(len(value))
			return original, nil
		}
	}
	// Errors are recorded, not swallowed. Continue draining the pipe even if the
	// disk fails; callers receive a failed receipt instead of breaking SIGPIPE.
	return original, nil
}

func (output *managedEndpointOutput) snapshot() (string, uint64, uint64, bool, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.file != nil && !output.closed && output.writeError == nil {
		if err := output.file.Sync(); err != nil {
			output.writeError = err
		}
	}
	return output.prefix.String(), output.sequence, output.dropped, output.closed, output.writeError
}

func (output *managedEndpointOutput) Close() error {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.closed {
		return output.writeError
	}
	output.closed = true
	if output.file != nil {
		output.writeError = errors.Join(output.writeError, output.file.Sync(), output.file.Close())
	}
	return output.writeError
}

// Atomic, bounded private receipts are restart evidence. They never contain
// an environment map and cannot authorize replay of a missing action.
func writeManagedEndpointPrivateJSON(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > 256<<10 {
		return errors.New("managed endpoint private receipt exceeds its protocol budget")
	}
	if !filepath.IsAbs(path) {
		return errors.New("managed endpoint receipt path is not absolute")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed endpoint receipt target is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Match the existing runtime's atomic-file publication discipline: an
	// exclusive random staging inode cannot be blocked by a crashed old writer.
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	installed := false
	defer func() {
		_ = file.Close()
		if !installed {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	installed = true
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readManagedEndpointPrivateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed endpoint private receipt is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil {
		return err
	} else if !opened.Mode().IsRegular() || opened.Size() > 256<<10 || !os.SameFile(info, opened) {
		return errors.New("invalid managed endpoint private receipt")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 256<<10+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("managed endpoint private receipt contains trailing data")
	}
	return nil
}

// The CLI reads only the private server-created descriptor to locate the
// original initialized workspace; a script or model cannot select a database.
func ManagedEndpointOwnerWorkspacePath(descriptor string) (string, error) {
	var instance ManagedEndpointOwnerInstance
	if err := readManagedEndpointPrivateJSON(descriptor, &instance); err != nil {
		return "", err
	}
	if err := instance.Validate(); err != nil {
		return "", err
	}
	if descriptor != filepath.Join(instance.RuntimeRoot, instance.ID, "owner.json") {
		return "", ErrManagedEndpointCorruptOwner
	}
	return instance.WorkspacePath, nil
}
