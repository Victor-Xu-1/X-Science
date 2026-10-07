//go:build linux || darwin

package transfer

import (
	"errors"
	"golang.org/x/sys/unix"
	"math"
)

func AvailableStorage(directory string) (Capacity, error) {
	var info unix.Statfs_t
	if err := unix.Statfs(directory, &info); err != nil {
		return Capacity{}, err
	}
	if info.Bsize <= 0 || uint64(info.Bavail) > math.MaxInt64/uint64(info.Bsize) || uint64(info.Ffree) > math.MaxInt64 {
		return Capacity{}, errors.New("storage capacity is not representable")
	}
	return Capacity{BytesKnown: true, Bytes: int64(info.Bavail) * int64(info.Bsize), FilesKnown: true, Files: int64(info.Ffree)}, nil
}
