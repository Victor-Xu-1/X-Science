//go:build !linux

package compute

import (
	"context"
	"errors"
)

func NewManagedEndpointSystemRuntime(root, executable string, store ManagedEndpointOwnerStore) (ManagedEndpointOwnerRuntime, error) {
	return nil, errors.New("restart-surviving managed endpoint ownership requires Linux/WSL")
}
func RunManagedEndpointOwnerWorker(context.Context, string, ManagedEndpointOwnerStore) error {
	return errors.New("managed endpoint owner worker requires Linux/WSL")
}
