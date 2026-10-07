//go:build !linux && !darwin

package transfer

import "errors"

func AvailableStorage(directory string) (Capacity, error) {
	return Capacity{}, errors.New("native storage capacity observation is unavailable on this host")
}
