package transfer

import (
	"errors"
	"io"
)

// StorageWriter bounds writes by observed physical space, not dataset size.
// The reserve protects metadata/control-plane recovery on a shared volume.
type StorageWriter struct {
	Writer    io.Writer
	Directory string
	Reserve   int64
}

func (w StorageWriter) Write(data []byte) (int, error) {
	capacity, err := AvailableStorage(w.Directory)
	if err != nil {
		return 0, err
	}
	if w.Writer == nil || !capacity.BytesKnown || w.Reserve < 0 || int64(len(data)) > max(0, capacity.Bytes-w.Reserve) {
		return 0, errors.New("compute transfer is waiting for physical storage capacity")
	}
	return w.Writer.Write(data)
}
