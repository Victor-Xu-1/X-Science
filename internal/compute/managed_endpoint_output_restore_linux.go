//go:build linux

package compute

import (
	"errors"
	"io"
	"os"
)

func reopenManagedEndpointOutput(dir string, policy ManagedEndpointOutputPolicy) (*managedEndpointOutput, error) {
	if err := ensureManagedEndpointPrivateDirectory(dir); err != nil {
		return nil, err
	}
	output := &managedEndpointOutput{dir: dir, policy: policy, retained: make([]int64, policy.Segments)}
	for index := 0; index < policy.Segments; index++ {
		info, err := os.Lstat(output.segmentPath(index))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > policy.SegmentBytes {
			return nil, errors.New("managed endpoint retained output is corrupt or exceeds its policy")
		}
		output.retained[index] = info.Size()
		output.total += uint64(info.Size())
	}
	file, err := os.OpenFile(output.segmentPath(0), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(output.retained[0], io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	output.file, output.used = file, output.retained[0]
	return output, nil
}
