//go:build linux

package compute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func bootstrapManagedEndpointWorker(ctx context.Context, input ManagedEndpointBootstrap, store ManagedEndpointOwnerStore) (ManagedEndpointOwnerInstance, *managedEndpointUnit, ManagedEndpointRuntimeIdentity, string, error) {
	var instance ManagedEndpointOwnerInstance
	var identity ManagedEndpointRuntimeIdentity
	if ctx == nil {
		return instance, nil, identity, "", errors.New("managed endpoint worker context is unavailable")
	}
	if err := input.Validate(); err != nil {
		return instance, nil, identity, "", err
	}
	if store == nil {
		return instance, nil, identity, "", errors.New("managed endpoint worker has no durable admission authority")
	}
	// This unlocked read is only preparation for physical observation. The
	// canonical writer below re-reads everything; no newer ref may be borrowed.
	prepared, _, err := store.ReadManagedEndpointAction(ctx, input.RequestedAction)
	if err != nil {
		return instance, nil, identity, "", err
	}
	if !input.Matches(prepared) {
		return instance, nil, identity, "", ErrManagedEndpointCorruptOwner
	}
	unit, err := newManagedEndpointUnit()
	if err != nil {
		return instance, nil, identity, "", err
	}
	observed, err := unit.observe(ctx, prepared)
	if err != nil {
		return instance, nil, identity, "", err
	}
	if observed.Identity.PID != int64(os.Getpid()) {
		return instance, nil, identity, "", errors.New("managed endpoint worker is not the positively identified unit owner")
	}
	var socket string
	published := false
	err = store.WithManagedEndpointBootstrapPublication(ctx, input, observed.Identity, func(current ManagedEndpointOwnerInstance, _ ManagedEndpointAction) error {
		if !input.Matches(current) {
			return ErrManagedEndpointCorruptOwner
		}
		path, err := publishManagedEndpointBootstrap(current)
		if err != nil {
			return err
		}
		instance, identity, socket, published = current, observed.Identity, path, true
		return nil
	})
	if err != nil {
		return instance, nil, identity, "", err
	}
	if !published {
		return instance, nil, identity, "", ErrManagedEndpointStaleAction
	}
	return instance, unit, identity, socket, nil
}

// Sole descriptor/socket-parent publisher. Its caller holds canonical SQLite
// writer authority until these small synchronous filesystem operations finish.
// Unit control, physical probing, output-history recovery and listening remain
// outside that lock. Physical Stop still fences the identified worker's lifetime.
func publishManagedEndpointBootstrap(instance ManagedEndpointOwnerInstance) (string, error) {
	if err := ensureManagedEndpointPrivateDirectory(instance.RuntimeRoot); err != nil {
		return "", err
	}
	directory := managedEndpointInstanceDirectory(instance)
	if err := ensureManagedEndpointPrivateDirectory(directory); err != nil {
		return "", err
	}
	descriptor := filepath.Join(directory, "owner.json")
	var existing ManagedEndpointOwnerInstance
	if err := readManagedEndpointPrivateJSON(descriptor, &existing); err == nil {
		input, err := ManagedEndpointBootstrapFor(instance, instance.InitialAction)
		if err != nil {
			return "", err
		}
		if !input.Matches(existing) {
			return "", ErrManagedEndpointCorruptOwner
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeManagedEndpointPrivateJSON(descriptor, instance); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	socket, err := managedEndpointSocketPath(instance)
	if err != nil {
		return "", err
	}
	if err := ensureManagedEndpointPrivateDirectory(filepath.Dir(socket)); err != nil {
		return "", err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return "", ErrManagedEndpointCorruptOwner
		}
		// The CLI is this exact positively identified private unit incarnation.
		if err := os.Remove(socket); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return socket, nil
}
